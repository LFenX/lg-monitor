//go:build windows

package winsvc

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

type Runtime struct {
	Logger   *slog.Logger
	Ready    func()
	Identity string
}

func IsService() (bool, error) { return svc.IsWindowsService() }

type serviceEvents struct {
	log    *eventlog.Log
	logger *slog.Logger
}

func (e serviceEvents) Info(message string) error {
	e.logger.Info(message)
	return e.log.Info(1, message)
}
func (e serviceEvents) Error(message string) error {
	e.logger.Error(message)
	return e.log.Error(2, message)
}

type nativeReporter struct{ changes chan<- svc.Status }

func (r nativeReporter) Report(status RuntimeStatus) error {
	state := svc.StartPending
	accepts := svc.Accepted(0)
	switch status.State {
	case Running:
		state, accepts = svc.Running, svc.AcceptStop|svc.AcceptShutdown
	case StopPending:
		state = svc.StopPending
	}
	r.changes <- svc.Status{State: state, Accepts: accepts, CheckPoint: status.CheckPoint, WaitHint: uint32(status.WaitHint / time.Millisecond)}
	return nil
}

type nativeHost struct {
	ctx      context.Context
	run      func(context.Context, Runtime) error
	logger   *slog.Logger
	events   Events
	identity string
	err      error
}

func (h *nativeHost) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	controls := make(chan Control, 1)
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		defer close(controls)
		for {
			select {
			case <-finished:
				return
			case request, open := <-requests:
				if !open {
					return
				}
				var control Control
				switch request.Cmd {
				case svc.Stop:
					control = ControlStop
				case svc.Shutdown:
					control = ControlShutdown
				case svc.Interrogate:
					control = ControlInterrogate
				default:
					continue
				}
				select {
				case controls <- control:
				case <-finished:
					return
				}
			}
		}
	}()
	h.err = Supervise(h.ctx, HostOptions{Requests: controls, Reporter: nativeReporter{changes}, Events: h.events, Run: func(ctx context.Context, ready func()) error {
		var once sync.Once
		return h.run(ctx, Runtime{Logger: h.logger, Identity: h.identity, Ready: func() { once.Do(ready) }})
	}})
	if h.err != nil {
		return true, 1
	}
	return false, 0
}

func RunService(ctx context.Context, home string, level slog.Level, run func(context.Context, Runtime) error) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("读取服务账户身份失败：%w", err)
	}
	if !user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		return fmt.Errorf("lgmon 服务必须以 LocalSystem 运行，请重新安装服务")
	}
	logger, file, err := openServiceLog(home, level)
	if err != nil {
		return err
	}
	defer file.Close()
	previousLogger := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(previousLogger)
	eventLog, err := eventlog.Open(Name)
	if err != nil {
		logger.Error("Could not open service event source", "error", err)
		return fmt.Errorf("打开 lgmon 事件日志来源失败，请重新安装服务：%w", err)
	}
	defer eventLog.Close()
	host := &nativeHost{ctx: ctx, run: run, logger: logger, events: serviceEvents{eventLog, logger}, identity: "LocalSystem"}
	if err := svc.Run(Name, host); err != nil {
		_ = host.events.Error("Service dispatcher failed: " + err.Error())
		return fmt.Errorf("运行 Windows 服务宿主失败：%w", err)
	}
	return host.err
}

func openServiceLog(home string, level slog.Level) (*slog.Logger, *os.File, error) {
	directory := filepath.Join(home, "logs")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, nil, fmt.Errorf("创建服务日志目录失败：%w", err)
	}
	file, err := os.OpenFile(filepath.Join(directory, "lgmon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("打开服务日志失败：%w", err)
	}
	logger := slog.New(slog.NewJSONHandler(file, &slog.HandlerOptions{Level: level, ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
		if attr.Key == slog.TimeKey && attr.Value.Kind() == slog.KindTime {
			return slog.Int64(slog.TimeKey, attr.Value.Time().UnixMilli())
		}
		return attr
	}}))
	return logger, file, nil
}
