package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/app"
	"github.com/LFenX/lg-monitor/internal/config"
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
}

func defaultOpenDependencies(run localRunner) openDependencies {
	return openDependencies{
		Panel: func(home string) (string, error) {
			settings, err := config.LoadSettings(os.DirFS(filepath.Join(home, "config")))
			if err != nil {
				return "", err
			}
			return app.TrayMonitorURL(settings.HTTPAddress())
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
			return openService{Installed: status.Installed, State: status.State, Home: definition.Home, Role: definition.Role}, err
		},
		Window: app.OpenAppWindow,
		Run:    run,
		Notice: showNotice,
		Detach: detachOwnConsole,
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

func openLgmon(cmd *cobra.Command, home string, deps openDependencies) error {
	ctx := cmd.Context()
	if deps.Detach != nil {
		deps.Detach()
	}
	address, panelErr := deps.Panel(home)
	if panelErr == nil && deps.Alive(ctx, address) {
		return deps.Window(ctx, address)
	}
	// Only the service installed for this home stands in for it; one serving another home is someone else's.
	if service, err := deps.Service(ctx); err == nil && service.Installed && sameHome(service.Home, home) {
		message := "这台电脑上的 lgmon 只负责采集，没有看板。\n\n请在监控器那台电脑上打开 lgmon。"
		if service.Role != "collector" {
			// A service that was just started answers within a few seconds.
			if panelErr == nil && service.State != winsvc.Stopped {
				for range 20 {
					if err := deps.Wait(ctx, 500*time.Millisecond); err != nil {
						return nil
					}
					if deps.Alive(ctx, address) {
						return deps.Window(ctx, address)
					}
				}
			}
			message = "lgmon 服务已经安装，但现在没有响应。\n\n请在 Windows 的“服务”里启动 lgmon，或者在管理员 PowerShell 里运行：\nlgmon service start"
		}
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
