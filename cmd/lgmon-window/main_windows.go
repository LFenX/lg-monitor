//go:build windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/desktop"
	"github.com/LFenX/lg-monitor/internal/webwindow"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// systemProxy is the proxy set in Windows' own settings, which tools like Clash fill in; the environment's
// HTTPS_PROXY still wins when it is set.
func systemProxy() *url.URL {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer key.Close()
	if enabled, _, err := key.GetIntegerValue("ProxyEnable"); err != nil || enabled == 0 {
		return nil
	}
	server, _, err := key.GetStringValue("ProxyServer")
	if err != nil || strings.TrimSpace(server) == "" {
		return nil
	}
	// "https=host:port;http=host:port" names one per scheme; a plain "host:port" serves them all.
	for _, part := range strings.Split(server, ";") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(part), "https="); ok {
			server = value
			break
		}
	}
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}
	address, err := url.Parse(server)
	if err != nil || address.Host == "" {
		return nil
	}
	return address
}

func updateClient() *http.Client {
	fallback := systemProxy()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = func(request *http.Request) (*url.URL, error) {
		if address, err := http.ProxyFromEnvironment(request); address != nil || err != nil {
			return address, err
		}
		// Like Windows' own "bypass for local addresses": this computer is never reached through a proxy.
		host := request.URL.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) || !strings.Contains(host, ".") {
			return nil, nil
		}
		return fallback, nil
	}
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: transport}
}

func run(args []string, stderr io.Writer) int {
	opts, err := parseOptions(args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	// Started by a service or as SYSTEM there is nobody to show it to, and WebView2 would only show an error box.
	if desktop.NoDesktop() {
		fmt.Fprintln(stderr, "lgmon 窗口只在登录用户的桌面上打开")
		return 0
	}
	className := opts.windowClass()
	if opts.updateAction != "" {
		if err := activateUpdater(opts); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	name, _ := windows.UTF16PtrFromString(`Local\` + className)
	mutex, err := windows.CreateMutex(nil, false, name)
	if mutex == 0 {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer windows.CloseHandle(mutex)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		// An overlapping launch may own the mutex before it has created the window.
		for range 80 {
			if webwindow.FocusExisting(className) {
				return 0
			}
			time.Sleep(50 * time.Millisecond)
		}
		fmt.Fprintln(stderr, "已有的 lgmon 小窗暂时没有响应，请重新打开")
		return 1
	}
	// Without the WebView2 runtime (rare on Windows 10/11) the same page opens as an Edge app window.
	if !webwindow.Available() {
		return fallback(opts.start.String())
	}
	start := opts.start
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := &updater{current: version, feed: updateFeed(), dir: updateDir(), client: updateClient()}
	err = webwindow.Run(webwindow.Options{
		ClassName: className, Title: "lgmon", Width: windowWidth, Height: windowHeight,
		MinWidth: minWidth, MinHeight: minHeight, Resizable: true,
		DataDir: opts.data, URL: start.String(), Script: bridge,
		OnActivate: func(download bool) { go updates.checkForUpdate(ctx, true, download) },
		OnReady: func(window *webwindow.Window) {
			updates.mu.Lock()
			updates.notify = func(state map[string]any) { window.Send(state) }
			updates.mu.Unlock()
			// An open window keeps looking, so a new release shows up without reopening lgmon.
			go func() {
				ticker := time.NewTicker(recheck)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						updates.check(ctx, true)
					}
				}
			}()
		},
		OnMessage: func(window *webwindow.Window, text string) {
			var request struct {
				Open   string `json:"open"`
				Reveal string `json:"reveal"`
				Type   string `json:"type"`
			}
			if json.Unmarshal([]byte(text), &request) != nil {
				return
			}
			switch request.Type {
			case "update-check":
				go updates.check(ctx, false)
				return
			case "update-refresh":
				go updates.check(ctx, true)
				return
			case "update-download":
				go updates.download(ctx)
				return
			case "update-notes":
				if page := updates.page(); strings.HasPrefix(page, "https://") || strings.HasPrefix(page, "http://") {
					_ = desktop.ShellOpen(page)
				}
				return
			case "update-apply":
				// The installer shows its own progress, updates the program this window runs from, and opens
				// lgmon again; the window closes so nothing holds the old files.
				if path, ok := updates.ready(); ok {
					cmd := exec.Command(path, "--update")
					if err := cmd.Start(); err != nil {
						updates.set(func() { updates.message = fmt.Sprintf("启动更新安装程序失败：%v", err) })
					} else {
						_ = cmd.Process.Release()
						window.Close()
					}
				}
				return
			}
			if request.Open == "" {
				return
			}
			if target, ok := external(request.Open, start); ok {
				go reveal(ctx, target, request.Reveal)
			}
		},
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return fallback(start.String())
	}
	return 0
}

// The calling service waits only for this short-lived handoff, never for the user to close the window.
func activateUpdater(opts options) error {
	if !webwindow.Available() {
		return fmt.Errorf("更新小窗需要 Microsoft Edge WebView2 运行时")
	}
	class := opts.windowClass()
	if webwindow.ActivateExisting(class, opts.updateAction) {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位更新小窗失败：%w", err)
	}
	child := exec.Command(self, "--url", opts.start.String(), "--data", opts.data)
	if err := child.Start(); err != nil {
		return fmt.Errorf("启动更新小窗失败：%w", err)
	}
	defer child.Process.Release()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	retry := time.NewTicker(100 * time.Millisecond)
	defer retry.Stop()
	for {
		if webwindow.ActivateExisting(class, opts.updateAction) {
			return nil
		}
		select {
		case <-deadline.C:
			return fmt.Errorf("更新小窗未能就绪，请从开始菜单打开 lgmon 后重试")
		case <-retry.C:
		}
	}
}

func fallback(target string) int {
	if desktop.OpenEdgeApp(target, windowWidth, windowHeight) == nil || desktop.ShellOpen(target) == nil {
		return 0
	}
	return 1
}

// The broker owns the decision to open. A background tab can receive the route
// even when the OS declines to bring the browser to the foreground.
func reveal(ctx context.Context, target, nonce string) {
	if marker := desktop.RevealMarker(nonce); marker != "" {
		desktop.FocusMarked(ctx, marker, revealWait)
		return
	}
	_ = desktop.ShellOpen(target)
}
