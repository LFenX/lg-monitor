package winsvc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/LFenX/lg-monitor/internal/secrets"
)

func prepareServicePaths(policy secrets.PathPolicy, executable, home string) error {
	if err := policy.Check(executable); err != nil {
		return err
	}
	if err := policy.Check(filepath.Dir(executable)); err != nil {
		return err
	}
	return policy.ProtectTree(home, false)
}

type Control string

const (
	ControlStop        Control = "stop"
	ControlShutdown    Control = "shutdown"
	ControlInterrogate Control = "interrogate"
)

type RuntimeStatus struct {
	State      State
	CheckPoint uint32
	WaitHint   time.Duration
}

type Reporter interface{ Report(RuntimeStatus) error }
type Events interface {
	Info(string) error
	Error(string) error
}

type Ticker interface {
	Ticks() <-chan time.Time
	Stop()
}
type Clock interface{ NewTicker(time.Duration) Ticker }
type hostClock struct{}
type hostTicker struct{ *time.Ticker }

func (hostClock) NewTicker(delay time.Duration) Ticker { return hostTicker{time.NewTicker(delay)} }
func (t hostTicker) Ticks() <-chan time.Time           { return t.C }

type HostOptions struct {
	Run                       func(context.Context, func()) error
	Requests                  <-chan Control
	Reporter                  Reporter
	Events                    Events
	Clock                     Clock
	Now                       func() time.Time
	StartTimeout, StopTimeout time.Duration
}

func Supervise(parent context.Context, options HostOptions) (runErr error) {
	if options.Run == nil || options.Reporter == nil || options.Events == nil {
		return fmt.Errorf("服务宿主需要运行入口、状态报告和事件日志接口")
	}
	defer func() {
		if runErr != nil {
			_ = options.Events.Error("lgmon service failed: " + runErr.Error())
		}
	}()
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Clock == nil {
		options.Clock = hostClock{}
	}
	if options.StartTimeout == 0 {
		options.StartTimeout = time.Minute
	}
	if options.StopTimeout == 0 {
		options.StopTimeout = 30 * time.Second
	}
	if options.StartTimeout < 0 || options.StopTimeout < 0 {
		return fmt.Errorf("服务启动与停止时限必须为正数")
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	ticker := options.Clock.NewTicker(2 * time.Second)
	defer ticker.Stop()
	started, stopAt := options.Now(), time.Time{}
	state := RuntimeStatus{State: StartPending, CheckPoint: 1, WaitHint: 10 * time.Second}
	if err := options.Reporter.Report(state); err != nil {
		return fmt.Errorf("报告服务启动状态失败：%w", err)
	}
	if err := options.Events.Info("lgmon service starting"); err != nil {
		return fmt.Errorf("记录服务启动事件失败：%w", err)
	}
	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- fmt.Errorf("服务运行入口发生 panic：%v", recovered)
			}
		}()
		done <- options.Run(ctx, func() {
			select {
			case ready <- struct{}{}:
			default:
			}
		})
	}()
	requests, stopping := options.Requests, parent.Done()
	beginStop := func() error {
		if state.State == StopPending {
			return nil
		}
		cancel()
		stopAt = options.Now()
		stopping = nil
		state = RuntimeStatus{State: StopPending, CheckPoint: 1, WaitHint: 10 * time.Second}
		return options.Reporter.Report(state)
	}
	for {
		select {
		case <-stopping:
			if err := beginStop(); err != nil {
				return fmt.Errorf("报告服务停止状态失败：%w", err)
			}
		case request, open := <-requests:
			if !open {
				requests = nil
				if err := beginStop(); err != nil {
					return err
				}
				continue
			}
			switch request {
			case ControlStop, ControlShutdown:
				if err := beginStop(); err != nil {
					return fmt.Errorf("报告服务停止状态失败：%w", err)
				}
			case ControlInterrogate:
				if err := options.Reporter.Report(state); err != nil {
					return fmt.Errorf("报告服务状态失败：%w", err)
				}
			}
		case <-ready:
			if state.State != StartPending {
				continue
			}
			state = RuntimeStatus{State: Running}
			if err := options.Reporter.Report(state); err != nil {
				return fmt.Errorf("报告服务运行状态失败：%w", err)
			}
			if err := options.Events.Info("lgmon service running"); err != nil {
				return fmt.Errorf("记录服务就绪事件失败：%w", err)
			}
		case err := <-done:
			if state.State == StopPending || parent.Err() != nil {
				if err != nil && !errors.Is(err, context.Canceled) {
					_ = options.Events.Error("lgmon service shutdown returned an error: " + err.Error())
				}
				_ = options.Events.Info("lgmon service stopped")
				return nil
			}
			if err == nil {
				err = fmt.Errorf("服务运行入口在未收到停止请求时提前结束")
			}
			return err
		case <-ticker.Ticks():
			if state.State == StartPending && options.Now().Sub(started) >= options.StartTimeout {
				err := fmt.Errorf("服务启动超过 %s，请检查配置和日志", options.StartTimeout)
				return err
			}
			if state.State == StopPending && options.Now().Sub(stopAt) >= options.StopTimeout {
				// A requested stop must not trigger SCM recovery; durable work can resume on the next start.
				_ = options.Events.Error("lgmon service shutdown deadline reached; exiting after requested stop")
				return nil
			}
			if state.State == StartPending || state.State == StopPending {
				state.CheckPoint++
				if err := options.Reporter.Report(state); err != nil {
					return fmt.Errorf("报告服务进度失败：%w", err)
				}
			}
		}
	}
}
