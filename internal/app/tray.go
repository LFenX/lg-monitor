package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/store"
)

type TrayState string

const (
	TrayStarting    TrayState = "starting"
	TrayHealthy     TrayState = "healthy"
	TrayFiring      TrayState = "firing"
	TrayWarning     TrayState = "warning"
	TrayPaused      TrayState = "paused"
	TrayUnavailable TrayState = "unavailable"
)

type TrayAction uint8

const (
	TrayOpenPanel TrayAction = iota + 1
	TrayPauseHour
	TrayExit
)

type TrayStatus struct {
	Firing   int
	Critical int
	CanPause bool
	PanelURL string
	Pause    store.NotificationPause
	Help     string
}

type TrayView struct {
	State             TrayState
	Firing            int
	CanOpen, CanPause bool
	Message           string
	Until             int64
	Help              string
}

type TrayBackend interface {
	Status(context.Context) (TrayStatus, error)
	Pause(context.Context, time.Duration) error
	Resume(context.Context) error
}

type TrayOpener interface {
	Open(context.Context, string) error
}

type TrayDriver interface {
	Run(context.Context, func()) error
	Events() <-chan TrayAction
	Update(TrayView) error
}

type TrayOptions struct {
	Backend  TrayBackend
	Driver   TrayDriver
	Opener   TrayOpener
	Clock    collector.ProbeClock
	Interval time.Duration
	Logger   *slog.Logger
}

func RunTray(parent context.Context, opts TrayOptions) error {
	if opts.Backend == nil || opts.Driver == nil || opts.Opener == nil {
		return fmt.Errorf("托盘需要状态读取、界面和浏览器依赖")
	}
	if parent.Err() != nil {
		return nil
	}
	if opts.Interval < 0 {
		return fmt.Errorf("托盘刷新间隔必须为正数")
	}
	if opts.Interval == 0 {
		opts.Interval = 10 * time.Second
	}
	if opts.Clock == nil {
		opts.Clock = collector.NewProbeClock()
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	ready := make(chan struct{})
	var readyOnce sync.Once
	worker := make(chan error, 1)
	go func() {
		err := trayLoop(ctx, opts, ready)
		cancel()
		worker <- err
	}()
	driverErr := opts.Driver.Run(ctx, func() { readyOnce.Do(func() { close(ready) }) })
	cancel()
	if err := <-worker; err != nil {
		return err
	}
	if driverErr != nil {
		return fmt.Errorf("运行托盘失败：%w", driverErr)
	}
	select {
	case <-ready:
	default:
		if parent.Err() == nil {
			return fmt.Errorf("托盘在初始化完成前退出")
		}
	}
	return nil
}

type trayReply struct {
	action  TrayAction
	status  TrayStatus
	err     error
	resumed bool
}

func trayPanelURL(value string) bool {
	if strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") &&
		parsed.Hostname() != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func trayLoop(parent context.Context, opts TrayOptions, ready <-chan struct{}) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	select {
	case <-ctx.Done():
		return nil
	case <-ready:
	}
	ticker := opts.Clock.NewTicker(opts.Interval)
	defer ticker.Stop()
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	replies := make(chan trayReply, 3)
	var status TrayStatus
	var known, unavailable, refreshing, pausing, opening bool
	message := ""
	render := func() error {
		view := TrayView{State: TrayStarting, Message: message, Firing: status.Firing, CanOpen: trayPanelURL(status.PanelURL), Help: status.Help}
		if unavailable {
			view.State, view.Message = TrayUnavailable, "连不上监控器"
			if status.Help != "" {
				view.Message += "，" + status.Help
			}
		} else if known {
			view.State = TrayHealthy
			if status.Firing > 0 {
				view.State = TrayWarning
			}
			if status.Critical > 0 {
				view.State = TrayFiring
			}
			if status.Pause.Paused && status.Pause.Until != nil {
				view.State, view.Until = TrayPaused, *status.Pause.Until
			}
			view.CanPause = status.CanPause && !pausing
		}
		if err := opts.Driver.Update(view); err != nil {
			return fmt.Errorf("更新托盘状态失败：%w", err)
		}
		return nil
	}
	request := func(action TrayAction, panel string) {
		resume := status.Pause.Paused
		workers.Add(1)
		go func() {
			defer workers.Done()
			call, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			reply := trayReply{action: action, resumed: resume}
			switch action {
			case 0:
				reply.status, reply.err = opts.Backend.Status(call)
				if reply.err == nil && (reply.status.Firing < 0 || !trayPanelURL(reply.status.PanelURL)) {
					reply.err = fmt.Errorf("监控器返回的托盘状态无效")
				}
			case TrayPauseHour:
				if resume {
					reply.err = opts.Backend.Resume(call)
				} else {
					reply.err = opts.Backend.Pause(call, time.Hour)
				}
			case TrayOpenPanel:
				reply.err = opts.Opener.Open(call, panel)
			}
			select {
			case replies <- reply:
			case <-ctx.Done():
			}
		}()
	}
	refresh := func() {
		if !refreshing {
			refreshing = true
			request(0, "")
		}
	}
	if err := render(); err != nil {
		return err
	}
	refresh()
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-ticker.Ticks():
			if !ok {
				return fmt.Errorf("托盘刷新时钟提前关闭")
			}
			refresh()
		case action, ok := <-opts.Driver.Events():
			if !ok {
				return fmt.Errorf("托盘操作流提前关闭")
			}
			switch action {
			case TrayExit:
				return nil
			case TrayOpenPanel:
				if !opening && trayPanelURL(status.PanelURL) {
					opening = true
					request(action, status.PanelURL)
				}
			case TrayPauseHour:
				if known && !unavailable && status.CanPause && !pausing {
					pausing = true
					request(action, "")
					if err := render(); err != nil {
						return err
					}
				}
			}
		case reply := <-replies:
			switch reply.action {
			case 0:
				refreshing = false
				if reply.err != nil {
					status.Help = reply.status.Help
					if reply.status.PanelURL != "" {
						status.PanelURL = reply.status.PanelURL
					}
					if !unavailable {
						opts.Logger.Warn("Tray status refresh failed")
					}
					unavailable = true
				} else {
					if unavailable {
						opts.Logger.Info("Tray connection restored")
					}
					status, known, unavailable = reply.status, true, false
				}
			case TrayPauseHour:
				pausing = false
				message = "上次操作：已暂停通知 1 小时，监控继续记录状态"
				if reply.resumed {
					message = "上次操作：已恢复通知"
				}
				if reply.err != nil {
					message = "暂停通知失败，请检查监控器连接和令牌权限"
					opts.Logger.Warn("Tray notification pause failed")
				}
				refresh()
			case TrayOpenPanel:
				opening = false
				if reply.err != nil {
					message = "打开面板失败，请手动在浏览器中打开监控器地址"
					opts.Logger.Warn("Tray panel launch failed")
				} else {
					message = ""
				}
			}
			if err := render(); err != nil {
				return err
			}
		}
	}
}
