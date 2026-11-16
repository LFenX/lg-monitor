//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"unsafe"

	"github.com/LFenX/lg-monitor/internal/desktop"
	"github.com/wailsapp/go-webview2/pkg/edge"
	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// className identifies lgmon's window, so a second launch brings the first one forward instead of opening another.
const className = "lgmonWindow"

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
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procShowWindow                    = user32.NewProc("ShowWindow")
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
	wmGetMinMaxInfo = 0x0024
	wmSetIcon       = 0x0080
	wmDpiChanged    = 0x02e0

	windowStyle = 0x00c00000 | 0x00080000 | 0x00040000 | 0x00020000 // caption, system menu, sizing frame, minimize

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

// The page's own background, so the title bar and the first frame before the page paints match it.
var (
	lightBackground = [3]uint8{0xfa, 0xf9, 0xf5} // --bg in web/src/lgmon.css
	darkBackground  = [3]uint8{0x26, 0x26, 0x24}
	lightText       = [3]uint8{0x14, 0x14, 0x13} // --text
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

func run(args []string, stderr io.Writer) int {
	opts, err := parseOptions(args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if focusExisting() {
		return 0
	}
	// Without the WebView2 runtime (rare on Windows 10/11) the same page opens as an Edge app window.
	if _, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString(""); err != nil {
		return fallback(opts.start.String())
	}
	if err := showWindow(opts); err != nil {
		fmt.Fprintln(stderr, err)
		return fallback(opts.start.String())
	}
	return 0
}

func fallback(target string) int {
	if desktop.OpenEdgeApp(target, windowWidth, windowHeight) == nil || desktop.ShellOpen(target) == nil {
		return 0
	}
	return 1
}

func focusExisting() bool {
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

func scaled(value int, dpi uint32) int32 { return int32(value * int(dpi) / 96) }

func colorRef(color [3]uint8) uintptr {
	return uintptr(color[0]) | uintptr(color[1])<<8 | uintptr(color[2])<<16
}

func darkTheme() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	value, _, err := key.GetIntegerValue("AppsUseLightTheme")
	return err == nil && value == 0
}

func showWindow(opts options) error {
	// -4 is per-monitor DPI awareness v2: crisp text and the right size on every screen.
	procSetProcessDpiAwarenessContext.Call(^uintptr(3))
	instance, _, _ := procGetModuleHandleW.Call(0)
	dark := darkTheme()
	background, foreground := lightBackground, lightText
	if dark {
		background, foreground = darkBackground, darkText
	}
	var view *edge.Chromium
	ready := false
	procedure := windows.NewCallback(func(window, msg, wParam, lParam uintptr) uintptr {
		switch msg {
		case wmSize:
			if ready {
				view.Resize()
			}
			return 0
		case wmMove:
			if ready {
				_ = view.NotifyParentWindowPositionChanged()
			}
		case wmSetFocus:
			if ready {
				view.Focus()
			}
		case wmGetMinMaxInfo:
			dpi, _, _ := procGetDpiForWindow.Call(window)
			if dpi == 0 {
				dpi = 96
			}
			info := *(**minMaxInfo)(unsafe.Pointer(&lParam))
			info.MinTrackSize = point{scaled(minWidth, uint32(dpi)), scaled(minHeight, uint32(dpi))}
			return 0
		case wmDpiChanged:
			// Windows suggests where the window should be at the new scale; take it as given.
			suggested := *(**rect)(unsafe.Pointer(&lParam))
			procSetWindowPos.Call(window, 0, uintptr(suggested.Left), uintptr(suggested.Top),
				uintptr(suggested.Right-suggested.Left), uintptr(suggested.Bottom-suggested.Top), swpNoZOrder|swpNoActivate)
			return 0
		case wmDestroy:
			procPostQuitMessage.Call(0)
			return 0
		}
		result, _, _ := procDefWindowProcW.Call(window, msg, wParam, lParam)
		return result
	})

	dpi, _, _ := procGetDpiForSystem.Call()
	if dpi == 0 {
		dpi = 96
	}
	bigIcon, smallIcon := createIcon(int(scaled(32, uint32(dpi)))), createIcon(int(scaled(16, uint32(dpi))))
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	brush, _, _ := procCreateSolidBrush.Call(colorRef(background))
	name, _ := windows.UTF16PtrFromString(className)
	class := windowClass{Procedure: procedure, Instance: instance, Icon: bigIcon, Cursor: cursor, Background: brush, ClassName: name, SmallIcon: smallIcon}
	class.Size = uint32(unsafe.Sizeof(class))
	if atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		return fmt.Errorf("注册 lgmon 窗口失败：%w", err)
	}

	var area rect
	procSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&area)), 0)
	frame := rect{Right: scaled(windowWidth, uint32(dpi)), Bottom: scaled(windowHeight, uint32(dpi))}
	procAdjustWindowRectExForDpi.Call(uintptr(unsafe.Pointer(&frame)), windowStyle, 0, 0, dpi)
	width, height := frame.Right-frame.Left, frame.Bottom-frame.Top
	if room := area.Bottom - area.Top - scaled(24, uint32(dpi)); room > 0 && height > room {
		height = room
	}
	x := area.Left + (area.Right-area.Left-width)/2
	y := area.Top + (area.Bottom-area.Top-height)/2
	title, _ := windows.UTF16PtrFromString("lgmon")
	window, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(title)), windowStyle,
		uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0, 0, instance, 0)
	if window == 0 {
		return fmt.Errorf("创建 lgmon 窗口失败：%w", err)
	}
	procSendMessageW.Call(window, wmSetIcon, 1, bigIcon)
	procSendMessageW.Call(window, wmSetIcon, 0, smallIcon)
	// Windows 11 colours the title bar like the page; older systems ignore these and keep their own.
	darkMode := uint32(0)
	if dark {
		darkMode = 1
	}
	caption, text := uint32(colorRef(background)), uint32(colorRef(foreground))
	procDwmSetWindowAttribute.Call(window, dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&darkMode)), 4)
	procDwmSetWindowAttribute.Call(window, dwmwaCaptionColor, uintptr(unsafe.Pointer(&caption)), 4)
	procDwmSetWindowAttribute.Call(window, dwmwaTextColor, uintptr(unsafe.Pointer(&text)), 4)
	procShowWindow.Call(window, swShow)
	procUpdateWindow.Call(window)

	view = edge.NewChromium()
	view.DataPath = opts.data
	start := opts.start
	// The library exits after reporting an error; the page then opens in Edge so the person still sees it.
	view.SetErrorCallback(func(err error) {
		fmt.Fprintln(os.Stderr, "lgmon 窗口出错：", err)
		fallback(start.String())
	})
	view.MessageCallback = func(text string, _ *edge.ICoreWebView2, _ *edge.ICoreWebView2WebMessageReceivedEventArgs) {
		var request struct {
			Open string `json:"open"`
		}
		if json.Unmarshal([]byte(text), &request) != nil {
			return
		}
		if target, ok := external(request.Open, start); ok {
			_ = desktop.ShellOpen(target)
		}
	}
	if !view.Embed(window) {
		return fmt.Errorf("WebView2 没有启动")
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
	view.Init(bridge)
	view.Navigate(start.String())

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
	pixels := drawMark(size)
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
