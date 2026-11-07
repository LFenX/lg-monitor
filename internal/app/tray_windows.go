//go:build windows

package app

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"fyne.io/systray"
	"github.com/LFenX/lg-monitor/internal/winsvc"
	"golang.org/x/sys/windows"
)

var nativeTrayStarted atomic.Bool
var trayUser32 = windows.NewLazySystemDLL("user32.dll")
var trayPeekMessage = trayUser32.NewProc("PeekMessageW")
var trayPostThreadMessage = trayUser32.NewProc("PostThreadMessageW")

type trayWindowsMessage struct {
	Window         windows.Handle
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}

type trayEnvironment interface {
	IsService() (bool, error)
	SessionID() (uint32, error)
}

type nativeTrayEnvironment struct{}

func (nativeTrayEnvironment) IsService() (bool, error) { return winsvc.IsService() }
func (nativeTrayEnvironment) SessionID() (uint32, error) {
	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil {
		return 0, fmt.Errorf("读取托盘进程会话失败：%w", err)
	}
	return session, nil
}

func trayInteractive(environment trayEnvironment) error {
	service, err := environment.IsService()
	if err != nil {
		return fmt.Errorf("检查托盘运行上下文失败：%w", err)
	}
	if service {
		return fmt.Errorf("Windows 服务不显示托盘，请在已登录的用户桌面单独运行")
	}
	session, err := environment.SessionID()
	if err != nil {
		return fmt.Errorf("检查托盘用户会话失败：%w", err)
	}
	if session == 0 {
		return fmt.Errorf("会话 0 不显示托盘，请在已登录的用户桌面运行")
	}
	return nil
}

type nativeTrayDriver struct {
	events      chan TrayAction
	icons       map[TrayState][]byte
	environment trayEnvironment
	mu          sync.Mutex
	open        *systray.MenuItem
	pause       *systray.MenuItem
	help        *systray.MenuItem
	quit        *systray.MenuItem
	view        TrayView
	known       bool
}

func NewNativeTrayDriver() (TrayDriver, error) {
	driver := &nativeTrayDriver{events: make(chan TrayAction, 8), icons: map[TrayState][]byte{}, environment: nativeTrayEnvironment{}}
	for _, state := range []TrayState{TrayStarting, TrayHealthy, TrayFiring, TrayWarning, TrayPaused, TrayUnavailable} {
		icon, err := trayIcon(state)
		if err != nil {
			return nil, err
		}
		driver.icons[state] = icon
	}
	return driver, nil
}

func (d *nativeTrayDriver) Events() <-chan TrayAction { return d.events }

func (d *nativeTrayDriver) Run(parent context.Context, readyCallback func()) error {
	if parent.Err() != nil {
		return nil
	}
	if err := trayInteractive(d.environment); err != nil {
		return err
	}
	if !nativeTrayStarted.CompareAndSwap(false, true) {
		return fmt.Errorf("同一进程只能启动一个托盘，请关闭后重新运行")
	}
	ctx, cancel := context.WithCancel(parent)
	ready, done, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
	thread := make(chan uint32, 1)
	defer func() { cancel(); close(released) }()
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		// Reserve the owned thread ID until every possible cancellation post has finished.
		defer func() { close(done); <-released }()
		var message trayWindowsMessage
		// A queue must exist before cancellation can post WM_QUIT to this owned thread.
		trayPeekMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0x0400, 0x0400, 0)
		thread <- windows.GetCurrentThreadId()
		if ctx.Err() != nil {
			return
		}
		systray.Run(func() {
			if ctx.Err() != nil {
				systray.Quit()
				return
			}
			d.mu.Lock()
			systray.SetIcon(d.icons[TrayStarting])
			systray.SetTooltip("lg-monitor：正在连接监控器")
			d.open = systray.AddMenuItem("打开面板", "在系统浏览器中打开监控面板")
			d.pause = systray.AddMenuItem("暂停通知 1 小时", "状态继续记录，暂停通知需要配置权限")
			d.help = systray.AddMenuItem("需要管理员运行一次 lgmon tray setup", "打开面板查看状态")
			d.help.Hide()
			d.quit = systray.AddMenuItem("退出", "退出当前程序")
			d.open.Disable()
			d.pause.Disable()
			d.mu.Unlock()
			close(ready)
		}, nil)
	}()
	id := <-thread
	startup := time.NewTimer(5 * time.Second)
	defer startup.Stop()
	select {
	case <-ready:
		if readyCallback != nil {
			readyCallback()
		}
	case <-done:
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("托盘初始化未完成，请在已登录的 Windows 桌面运行")
	case <-ctx.Done():
		return stopNativeTray(id, done)
	case <-startup.C:
		if err := stopNativeTray(id, done); err != nil {
			return fmt.Errorf("托盘初始化超时，清理失败：%w", err)
		}
		return fmt.Errorf("托盘初始化超时，请在已登录的 Windows 桌面运行")
	}
	for {
		var action TrayAction
		select {
		case <-ctx.Done():
			return stopNativeTray(id, done)
		case <-done:
			return nil
		case <-d.open.ClickedCh:
			action = TrayOpenPanel
		case <-d.help.ClickedCh:
			action = TrayOpenPanel
		case <-d.pause.ClickedCh:
			action = TrayPauseHour
		case <-d.quit.ClickedCh:
			action = TrayExit
		}
		select {
		case d.events <- action:
		case <-ctx.Done():
			return stopNativeTray(id, done)
		case <-done:
			return nil
		}
	}
}

func stopNativeTray(thread uint32, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	default:
	}
	systray.Quit()
	grace := time.NewTimer(2 * time.Second)
	defer grace.Stop()
	select {
	case <-done:
		return nil
	case <-grace.C:
	}
	// This fallback only targets the message queue created by this driver.
	ok, _, err := trayPostThreadMessage.Call(uintptr(thread), 0x0012, 0, 0)
	if ok == 0 {
		select {
		case <-done:
			return nil
		default:
			return fmt.Errorf("关闭托盘消息循环失败：%w", err)
		}
	}
	limit := time.NewTimer(time.Second)
	defer limit.Stop()
	select {
	case <-done:
		return fmt.Errorf("托盘未正常关闭，已结束本程序的消息循环")
	case <-limit.C:
		return fmt.Errorf("托盘退出超时，请关闭当前 lgmon 程序后重试")
	}
}

func (d *nativeTrayDriver) Update(view TrayView) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.open == nil || d.pause == nil || d.quit == nil {
		return fmt.Errorf("托盘尚未初始化")
	}
	if d.known && d.view == view {
		return nil
	}
	if !d.known || d.view.State != view.State {
		systray.SetIcon(d.icons[view.State])
	}
	title := "lg-monitor：正在连接监控器"
	switch view.State {
	case TrayFiring, TrayWarning, TrayHealthy:
		title = fmt.Sprintf("lg-monitor：正在触发 %d 条告警", view.Firing)
	case TrayPaused:
		title = "lg-monitor：通知已暂停到 " + time.UnixMilli(view.Until).In(time.Local).Format("15:04")
	case TrayUnavailable:
		title = "lg-monitor：连不上监控器"
	}
	if view.Message != "" {
		title += "；" + view.Message
	}
	systray.SetTooltip(title)
	if view.CanOpen {
		d.open.Enable()
	} else {
		d.open.Disable()
	}
	if view.CanPause {
		d.pause.Enable()
	} else {
		d.pause.Disable()
	}
	d.pause.SetTitle("暂停通知 1 小时")
	if view.Until != 0 {
		d.pause.SetTitle("恢复通知（已暂停到 " + time.UnixMilli(view.Until).In(time.Local).Format("15:04") + "）")
	}
	if view.Help != "" {
		d.help.SetTitle(view.Help)
		d.help.Show()
	} else {
		d.help.Hide()
	}
	d.view, d.known = view, true
	return nil
}

type NativeTrayOpener struct{}

func (NativeTrayOpener) Open(ctx context.Context, address string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !trayPanelURL(address) {
		return fmt.Errorf("面板地址必须为不含凭据、查询参数和片段的 HTTP/HTTPS 地址")
	}
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return fmt.Errorf("转换浏览器操作失败：%w", err)
	}
	target, err := windows.UTF16PtrFromString(address)
	if err != nil {
		return fmt.Errorf("转换面板地址失败：%w", err)
	}
	if err := windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL); err != nil {
		return fmt.Errorf("打开系统浏览器失败：%w", err)
	}
	return nil
}
