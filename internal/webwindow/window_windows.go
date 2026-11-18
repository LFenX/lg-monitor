//go:build windows

package webwindow

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procPostMessageW                  = user32.NewProc("PostMessageW")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procShowWindow                    = user32.NewProc("ShowWindow")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procUpdateWindow                  = user32.NewProc("UpdateWindow")
	procSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	procFindWindowW                   = user32.NewProc("FindWindowW")
	procIsIconic                      = user32.NewProc("IsIconic")
	procSetWindowPos                  = user32.NewProc("SetWindowPos")
	procLoadCursorW                   = user32.NewProc("LoadCursorW")
	procSendMessageW                  = user32.NewProc("SendMessageW")
	procGetDpiForSystem               = user32.NewProc("GetDpiForSystem")
	procGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procSystemParametersInfoW         = user32.NewProc("SystemParametersInfoW")
	procAdjustWindowRectExForDpi      = user32.NewProc("AdjustWindowRectExForDpi")
	procCreateIconIndirect            = user32.NewProc("CreateIconIndirect")
	procCreateDIBSection              = gdi32.NewProc("CreateDIBSection")
	procCreateBitmap                  = gdi32.NewProc("CreateBitmap")
	procCreateSolidBrush              = gdi32.NewProc("CreateSolidBrush")
	procDwmSetWindowAttribute         = dwmapi.NewProc("DwmSetWindowAttribute")
	procGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmDestroy       = 0x0002
	wmMove          = 0x0003
	wmSize          = 0x0005
	wmSetFocus      = 0x0007
	wmClose         = 0x0010
	wmGetMinMaxInfo = 0x0024
	wmSetIcon       = 0x0080
	wmDpiChanged    = 0x02e0
	// wmDispatch runs work queued from other goroutines on the window's own thread, where WebView2 must be used.
	wmDispatch = 0x8001

	styleDialog    = 0x00c00000 | 0x00080000  // caption and system menu: only the close button
	styleFixed     = styleDialog | 0x00020000 // and minimize
	styleResizable = styleFixed | 0x00040000  // and a sizing frame

	swShow         = 5
	swRestore      = 9
	idcArrow       = 32512
	spiGetWorkArea = 0x0030
	swpNoZOrder    = 0x0004
	swpNoActivate  = 0x0010

	dwmwaUseImmersiveDarkMode = 20
	dwmwaCaptionColor         = 35
	dwmwaTextColor            = 36
)

// The pages' own background (--bg) and text (--text) in web/src/lgmon.css, so the title bar and the first frame
// before the page paints match it.
var (
	lightBackground = [3]uint8{0xfa, 0xf9, 0xf5}
	darkBackground  = [3]uint8{0x26, 0x26, 0x24}
	lightText       = [3]uint8{0x14, 0x14, 0x13}
	darkText        = [3]uint8{0xfa, 0xf9, 0xf5}
)

type point struct{ X, Y int32 }

type rect struct{ Left, Top, Right, Bottom int32 }

type message struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}

type windowClass struct {
	Size        uint32
	Style       uint32
	Procedure   uintptr
	ClassExtra  int32
	WindowExtra int32
	Instance    uintptr
	Icon        uintptr
	Cursor      uintptr
	Background  uintptr
	MenuName    *uint16
	ClassName   *uint16
	SmallIcon   uintptr
}

type minMaxInfo struct {
	Reserved, MaxSize, MaxPosition, MinTrackSize, MaxTrackSize point
}

// Options describe one window. Sizes are logical pixels; the window scales them to the screen.
type Options struct {
	// ClassName identifies the window, so a second launch can bring the first one forward.
	ClassName           string
	Title               string
	Width, Height       int
	MinWidth, MinHeight int
	Resizable           bool
	// Dialog shows only the close button, for a window that is a single step such as the installer.
	Dialog    bool
	DataDir   string
	URL       string
	HTML      string
	Script    string
	OnMessage func(window *Window, message string)
	OnReady   func(window *Window)
}

// Window is a running window. Its methods may be called from any goroutine.
type Window struct {
	handle uintptr
	view   *edge.Chromium
	mu     sync.Mutex
	queue  []func()
}

// ErrNoWebView means the WebView2 runtime was found but would not start in this window.
var ErrNoWebView = errors.New("WebView2 没有启动")

// Available reports whether the WebView2 runtime is installed; without it the caller falls back to a browser.
func Available() bool {
	version, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	return err == nil && version != ""
}

// FocusExisting brings an already open window of this class forward and reports whether there was one.
func FocusExisting(className string) bool {
	name, _ := windows.UTF16PtrFromString(className)
	window, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(name)), 0)
	if window == 0 {
		return false
	}
	if minimized, _, _ := procIsIconic.Call(window); minimized != 0 {
		procShowWindow.Call(window, swRestore)
	}
	procSetForegroundWindow.Call(window)
	return true
}

// Dark reports whether Windows apps use the dark theme.
func Dark() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue("AppsUseLightTheme")
	return err == nil && value == 0
}

// Dispatch runs fn on the window's thread.
func (w *Window) Dispatch(fn func()) {
	w.mu.Lock()
	w.queue = append(w.queue, fn)
	w.mu.Unlock()
	procPostMessageW.Call(w.handle, wmDispatch, 0, 0)
}

// Send delivers a message to the page as a "lgmon-host" event whose detail is data.
func (w *Window) Send(data any) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return
	}
	w.Dispatch(func() {
		w.view.Eval("window.dispatchEvent(new CustomEvent('lgmon-host', { detail: " + string(encoded) + " }))")
	})
}

// Eval runs script in the page.
func (w *Window) Eval(script string) {
	w.Dispatch(func() { w.view.Eval(script) })
}

// Close closes the window, which ends Run.
func (w *Window) Close() { procPostMessageW.Call(w.handle, wmClose, 0, 0) }

func (w *Window) drain() {
	w.mu.Lock()
	queue := w.queue
	w.queue = nil
	w.mu.Unlock()
	for _, fn := range queue {
		fn()
	}
}

func scaled(value int, dpi uint32) int32 { return int32(value * int(dpi) / 96) }

func colorRef(color [3]uint8) uintptr {
	return uintptr(color[0]) | uintptr(color[1])<<8 | uintptr(color[2])<<16
}

// Run shows the window and returns when it is closed. It must be called from the main goroutine, which the
// WebView2 library has already locked to the main thread.
func Run(opts Options) error {
	// -4 is per-monitor DPI awareness v2: crisp text and the right size on every screen.
	procSetProcessDpiAwarenessContext.Call(^uintptr(3))
	instance, _, _ := procGetModuleHandleW.Call(0)
	dark := Dark()
	background, foreground := lightBackground, lightText
	if dark {
		background, foreground = darkBackground, darkText
	}
	style := uintptr(styleFixed)
	if opts.Resizable {
		style = styleResizable
	}
	if opts.Dialog {
		style = styleDialog
	}
	window := &Window{}
	ready := false
	procedure := windows.NewCallback(func(handle, msg, wParam, lParam uintptr) uintptr {
		switch msg {
		case wmSize:
			if ready {
				window.view.Resize()
			}
			return 0
		case wmMove:
			if ready {
				_ = window.view.NotifyParentWindowPositionChanged()
			}
		case wmSetFocus:
			if ready {
				window.view.Focus()
			}
		case wmDispatch:
			if ready {
				window.drain()
			}
			return 0
		case wmGetMinMaxInfo:
			if opts.MinWidth > 0 {
				dpi, _, _ := procGetDpiForWindow.Call(handle)
				if dpi == 0 {
					dpi = 96
				}
				info := *(**minMaxInfo)(unsafe.Pointer(&lParam))
				info.MinTrackSize = point{scaled(opts.MinWidth, uint32(dpi)), scaled(opts.MinHeight, uint32(dpi))}
			}
			return 0
		case wmDpiChanged:
			// Windows suggests where the window should be at the new scale; take it as given.
			suggested := *(**rect)(unsafe.Pointer(&lParam))
			procSetWindowPos.Call(handle, 0, uintptr(suggested.Left), uintptr(suggested.Top),
				uintptr(suggested.Right-suggested.Left), uintptr(suggested.Bottom-suggested.Top), swpNoZOrder|swpNoActivate)
			return 0
		case wmDestroy:
			procPostQuitMessage.Call(0)
			return 0
		}
		result, _, _ := procDefWindowProcW.Call(handle, msg, wParam, lParam)
		return result
	})

	dpi, _, _ := procGetDpiForSystem.Call()
	if dpi == 0 {
		dpi = 96
	}
	bigIcon, smallIcon := createIcon(int(scaled(32, uint32(dpi)))), createIcon(int(scaled(16, uint32(dpi))))
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	brush, _, _ := procCreateSolidBrush.Call(colorRef(background))
	name, _ := windows.UTF16PtrFromString(opts.ClassName)
	class := windowClass{Procedure: procedure, Instance: instance, Icon: bigIcon, Cursor: cursor, Background: brush, ClassName: name, SmallIcon: smallIcon}
	class.Size = uint32(unsafe.Sizeof(class))
	if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		return fmt.Errorf("注册 lgmon 窗口失败：%w", err)
	}

	var area rect
	procSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&area)), 0)
	frame := rect{Right: scaled(opts.Width, uint32(dpi)), Bottom: scaled(opts.Height, uint32(dpi))}
	procAdjustWindowRectExForDpi.Call(uintptr(unsafe.Pointer(&frame)), style, 0, 0, dpi)
	width, height := frame.Right-frame.Left, frame.Bottom-frame.Top
	if room := area.Bottom - area.Top - scaled(24, uint32(dpi)); room > 0 && height > room {
		height = room
	}
	x := area.Left + (area.Right-area.Left-width)/2
	y := area.Top + (area.Bottom-area.Top-height)/2
	title, _ := windows.UTF16PtrFromString(opts.Title)
	handle, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(title)), style,
		uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0, 0, instance, 0)
	if handle == 0 {
		return fmt.Errorf("创建 lgmon 窗口失败：%w", err)
	}
	window.handle = handle
	procSendMessageW.Call(handle, wmSetIcon, 1, bigIcon)
	procSendMessageW.Call(handle, wmSetIcon, 0, smallIcon)
	// Windows 11 colours the title bar like the page; older systems ignore these and keep their own.
	darkMode := uint32(0)
	if dark {
		darkMode = 1
	}
	caption, text := uint32(colorRef(background)), uint32(colorRef(foreground))
	procDwmSetWindowAttribute.Call(handle, dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&darkMode)), 4)
	procDwmSetWindowAttribute.Call(handle, dwmwaCaptionColor, uintptr(unsafe.Pointer(&caption)), 4)
	procDwmSetWindowAttribute.Call(handle, dwmwaTextColor, uintptr(unsafe.Pointer(&text)), 4)
	procShowWindow.Call(handle, swShow)
	procUpdateWindow.Call(handle)

	view := edge.NewChromium()
	window.view = view
	view.DataPath = opts.DataDir
	view.MessageCallback = func(text string, _ *edge.ICoreWebView2, _ *edge.ICoreWebView2WebMessageReceivedEventArgs) {
		if opts.OnMessage != nil {
			opts.OnMessage(window, text)
		}
	}
	if !view.Embed(handle) {
		// Leave no empty frame behind: the caller usually falls back to something else.
		procDestroyWindow.Call(handle)
		return ErrNoWebView
	}
	ready = true
	view.Resize()
	if settings, err := view.GetSettings(); err == nil {
		_ = settings.PutAreDefaultContextMenusEnabled(false)
		_ = settings.PutAreDevToolsEnabled(false)
		_ = settings.PutIsZoomControlEnabled(false)
		_ = settings.PutIsStatusBarEnabled(false)
		_ = settings.PutIsPinchZoomEnabled(false)
		_ = settings.PutIsSwipeNavigationEnabled(false)
	}
	view.SetBackgroundColour(background[0], background[1], background[2], 255)
	if opts.Script != "" {
		view.Init(opts.Script)
	}
	if opts.HTML != "" {
		view.NavigateToString(opts.HTML)
	} else {
		view.Navigate(opts.URL)
	}
	if opts.OnReady != nil {
		opts.OnReady(window)
	}
	window.drain()

	var next message
	for {
		got, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&next)), 0, 0, 0)
		if int32(got) <= 0 {
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&next)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&next)))
	}
}

type bitmapHeader struct {
	Size                        uint32
	Width, Height               int32
	Planes, BitCount            uint16
	Compression, SizeImage      uint32
	XPerMeter, YPerMeter        int32
	ColorsUsed, ColorsImportant uint32
}

type iconInfo struct {
	IsIcon             int32
	HotspotX, HotspotY uint32
	Mask, Color        uintptr
}

// createIcon draws lgmon's mark into a 32-bit icon, so the window and taskbar show it without a resource file.
func createIcon(size int) uintptr {
	if size < 16 {
		size = 16
	}
	header := bitmapHeader{Width: int32(size), Height: -int32(size), Planes: 1, BitCount: 32}
	header.Size = uint32(unsafe.Sizeof(header))
	var bits unsafe.Pointer
	color, _, _ := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if color == 0 || bits == nil {
		return 0
	}
	pixels := DrawMark(size)
	target := unsafe.Slice((*byte)(bits), size*size*4)
	for index := 0; index < size*size; index++ {
		target[index*4+0] = pixels[index*4+2]
		target[index*4+1] = pixels[index*4+1]
		target[index*4+2] = pixels[index*4+0]
		target[index*4+3] = pixels[index*4+3]
	}
	// The alpha channel decides what shows; the mask only has to exist.
	mask := make([]byte, ((size+15)/16*2)*size)
	monochrome, _, _ := procCreateBitmap.Call(uintptr(size), uintptr(size), 1, 1, uintptr(unsafe.Pointer(&mask[0])))
	info := iconInfo{IsIcon: 1, Mask: monochrome, Color: color}
	icon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&info)))
	return icon
}
