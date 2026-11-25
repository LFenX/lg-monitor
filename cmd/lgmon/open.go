// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/app"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/ports"
	"github.com/LFenX/lg-monitor/internal/winsvc"
	"github.com/spf13/cobra"
)

/*
 * lgmon without a command means "open lgmon": someone double-clicked it, picked it from the Start menu or from
 * the installer's last page, and expects to see something. A running lgmon gets its small window; an installed
 * but silent service gets an explanation; otherwise lgmon starts for this person with its tray.
 */

// openService is the installed lgmon service as far as opening lgmon cares: whether it runs and for which home.
type openService struct {
	Installed  bool
	State      winsvc.State
	Home, Role string
}

type openDependencies struct {
	Panel   func(home string) (string, error)
	Alive   func(ctx context.Context, address string) bool
	Service func(ctx context.Context) (openService, error)
	Window  func(ctx context.Context, address string) error
	Run     localRunner
	Notice  func(message string)
	Detach  func()
	Wait    func(ctx context.Context, delay time.Duration) error
	// Start starts the installed service; errStartDeclined means the person said no.
	Start func(ctx context.Context) error
	// Failure is why the service last failed to run, when it failed just now, or "".
	Failure func(ctx context.Context) string
	// Installed is the home the installer recorded beside this program, or "" for a copy nobody installed.
	Installed func() string
}

// installedLaunchHome reads the Inno installer's UTF-16LE setup.ini next to the executable.
func installedLaunchHome(executable string) string {
	metadata, _ := winsvc.ReadSetupMetadata(filepath.Dir(executable))
	return metadata.Home
}

func defaultOpenDependencies(run localRunner) openDependencies {
	return openDependencies{
		Panel: func(home string) (string, error) {
			if resolved, ok := config.ResolveHome(home); ok {
				home = resolved
			}
			return panelAddress(os.DirFS(filepath.Join(home, "config")))
		},
		Alive: lgmonAnswers,
		Service: func(ctx context.Context) (openService, error) {
			definition, installed, err := winsvc.InstalledDefinition(ctx)
			if err != nil || !installed {
				return openService{}, err
			}
			controller, err := winsvc.NativeController()
			if err != nil {
				return openService{}, err
			}
			status, err := controller.Status(ctx)
			home := definition.Home
			if resolved, ok := config.ResolveHome(home); ok {
				home = resolved
			}
			return openService{Installed: status.Installed, State: status.State, Home: home, Role: definition.Role}, err
		},
		Window:  app.OpenAppWindow,
		Run:     run,
		Notice:  showNotice,
		Detach:  detachOwnConsole,
		Start:   startInstalledService,
		Failure: serviceFailure,
		Installed: func() string {
			executable, err := os.Executable()
			if err != nil {
				return ""
			}
			return installedLaunchHome(executable)
		},
		Wait: func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	}
}

// panelAddress is where the panel of the home with this config directory answers. The service's home is
// readable only by SYSTEM and administrators, so the window a person opens without elevation cannot read its
// settings; it then tries the default address, and openLgmon still checks that lgmon answers there.
func panelAddress(configDir fs.FS) (string, error) {
	settings, err := config.LoadSettings(configDir)
	if err != nil {
		if _, statErr := fs.Stat(configDir, "settings.yaml"); !errors.Is(statErr, fs.ErrPermission) {
			return "", err
		}
		settings = config.Settings{}
	}
	return app.TrayMonitorURL(settings.HTTPAddress())
}

// lgmonAnswers asks the address for lgmon's local session, which only lgmon answers with this shape.
func lgmonAnswers(ctx context.Context, address string) bool {
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(address, "/")+"/api/v1/session", nil)
	if err != nil {
		return false
	}
	// The check is about this computer; a proxy must not answer for it.
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	var session struct {
		Scope string `json:"scope"`
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	return err == nil && response.StatusCode == http.StatusOK && json.Unmarshal(data, &session) == nil && session.Scope != ""
}

// errStartDeclined means the person chose not to start the service.
var errStartDeclined = errors.New("没有启动 lgmon 服务")

// findPanel returns where lgmon answers: at its address, or at one of the ports it takes when Windows has
// reserved its own.
func findPanel(ctx context.Context, address string, alive func(context.Context, string) bool) (string, bool) {
	if alive(ctx, address) {
		return address, true
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return "", false
	}
	for _, host := range ports.Fallbacks(parsed.Host) {
		candidate := *parsed
		candidate.Host = host
		if alive(ctx, candidate.String()) {
			return candidate.String(), true
		}
	}
	return "", false
}

// reachablePanel keeps address unless nothing listens there on this computer and lgmon answers at one of its
// alternatives. It makes no request while the address works.
func reachablePanel(ctx context.Context, address string) string {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "http" {
		return address
	}
	if ip := net.ParseIP(parsed.Hostname()); parsed.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return address
	}
	connection, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", parsed.Host)
	if err == nil {
		_ = connection.Close()
		return address
	}
	if found, ok := findPanel(ctx, address, lgmonAnswers); ok {
		return found
	}
	return address
}

// openInstalled starts the service when it is not running and waits for its panel. Without a panel it returns why.
func openInstalled(ctx context.Context, address string, service openService, deps openDependencies) (string, string, error) {
	if service.State == winsvc.Stopped && deps.Start != nil {
		// Windows starts lgmon a while after logging in, so soon after a restart it is simply not up yet.
		if err := deps.Start(ctx); errors.Is(err, errStartDeclined) {
			return "", "", err
		}
	}
	for attempt := range openAttempts {
		if found, ok := findPanel(ctx, address, deps.Alive); ok {
			return found, "", nil
		}
		if err := deps.Wait(ctx, openPoll); err != nil {
			return "", "", err
		}
		// A service that failed and lies still has nothing more to wait for.
		if attempt > 0 && attempt%10 == 0 && deps.Failure != nil {
			if current, err := deps.Service(ctx); err == nil && current.State == winsvc.Stopped {
				if reason := deps.Failure(ctx); reason != "" {
					return "", reason, nil
				}
			}
		}
	}
	reason := ""
	if deps.Failure != nil {
		reason = deps.Failure(ctx)
	}
	return "", reason, nil
}

// silentMessage explains a service that does not answer, with the reason when it left one.
func silentMessage(reason string) string {
	if reason == "" {
		return "lgmon 服务已经安装，但现在没有响应。\n\n请在 Windows 的“服务”里重新启动 lgmon，或者在管理员 PowerShell 里运行：\nlgmon service start"
	}
	return "lgmon 服务没能启动。\n\n原因：" + reason + "\n\n处理后可以再打开一次，或者在管理员 PowerShell 里运行：\nlgmon service start"
}

const (
	openPoll = 500 * time.Millisecond
	// A start right after booting competes with everything else that starts.
	openAttempts = 60
)

func openLgmon(cmd *cobra.Command, home string, deps openDependencies) error {
	ctx := cmd.Context()
	if deps.Detach != nil {
		deps.Detach()
	}
	address, panelErr := deps.Panel(home)
	if panelErr == nil {
		if found, ok := findPanel(ctx, address, deps.Alive); ok {
			return deps.Window(ctx, found)
		}
	}
	// Only the service installed for this home stands in for it; one serving another home is someone else's.
	if service, err := deps.Service(ctx); err == nil && service.Installed && sameHome(service.Home, home) {
		message := "这台电脑上的 lgmon 只负责采集，没有看板。\n\n请在监控器那台电脑上打开 lgmon。"
		if service.Role != "collector" {
			reason := ""
			if panelErr == nil {
				found, why, err := openInstalled(ctx, address, service, deps)
				if err != nil {
					return nil
				}
				if found != "" {
					return deps.Window(ctx, found)
				}
				reason = why
			}
			message = silentMessage(reason)
		}
		deps.Notice(message)
		return errors.New(strings.ReplaceAll(message, "\n\n", ""))
	}
	// An installed copy whose service is gone (an install or update stopped part way, or the service was removed)
	// must not stand in for it: running here it holds the service's ports and database, and the next install
	// then fails to start the service it registers.
	if deps.Installed != nil && sameHome(deps.Installed(), home) {
		message := "lgmon 的后台服务不在了，可能是上次安装或更新没有完成。\n\n请重新运行 lgmon 安装程序（lgmon-setup），点“继续安装”。配置和数据都保留。"
		deps.Notice(message)
		return errors.New(strings.ReplaceAll(message, "\n\n", ""))
	}
	// Nothing runs yet: run lgmon for this person, with its tray, and show the window once the page answers.
	var once sync.Once
	return deps.Run(ctx, app.LocalOptions{Home: home, Version: version, Role: "both", Listen: "127.0.0.1:7443", Logger: commandLogger(cmd),
		HTTPReady: func(listen string) {
			once.Do(func() {
				if address, err := app.TrayMonitorURL(listen); err == nil {
					go func() { _ = deps.Window(ctx, address) }()
				}
			})
		},
		Notice: func(string) error { return nil }})
}

func sameHome(a, b string) bool {
	clean := func(path string) string {
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		return strings.ToLower(filepath.Clean(path))
	}
	return a != "" && clean(a) == clean(b)
}
