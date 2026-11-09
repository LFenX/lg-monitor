package collector

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type RestartServiceHandle interface {
	State() (string, error)
	Stop() error
	Start() error
	Close() error
}
type RestartServiceManager interface {
	Open(context.Context, string) (RestartServiceHandle, error)
}
type RestartWaiter interface {
	Wait(context.Context, time.Duration) error
}
type restartTimer struct{}

func (restartTimer) Wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type RestrictedServiceRestarter struct {
	Manager RestartServiceManager
	Waiter  RestartWaiter
}

func (r RestrictedServiceRestarter) Restart(ctx context.Context, name string) (result error) {
	if r.Manager == nil {
		return fmt.Errorf("服务控制管理器不可用")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("服务控制尚未开始：%w", err)
	}
	service, err := r.Manager.Open(ctx, name)
	if err != nil {
		return fmt.Errorf("打开已授权服务失败：%w", err)
	}
	defer func() {
		if err := service.Close(); err != nil && result == nil {
			result = fmt.Errorf("%w：释放服务控制句柄失败", ErrRestartUncertain)
		}
	}()
	state, err := service.State()
	if err != nil {
		return fmt.Errorf("读取服务状态失败：%w", err)
	}
	if state != "running" && state != "stopped" {
		return fmt.Errorf("服务处于过渡或暂停状态，本次未操作")
	}
	waiter := r.Waiter
	if waiter == nil {
		waiter = restartTimer{}
	}
	wait := func(want string) error {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			state, err := service.State()
			if err != nil {
				return err
			}
			if state == want {
				return nil
			}
			if err := waiter.Wait(ctx, 200*time.Millisecond); err != nil {
				return err
			}
		}
	}
	if state == "running" {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("服务控制尚未开始：%w", err)
		}
		if err := service.Stop(); err != nil {
			return errors.Join(ErrRestartUncertain, fmt.Errorf("发送服务停止控制失败：%w", err))
		}
		if err := wait("stopped"); err != nil {
			return errors.Join(ErrRestartUncertain, fmt.Errorf("等待服务停止失败：%w", err))
		}
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrRestartUncertain, err)
	}
	if err := service.Start(); err != nil {
		return errors.Join(ErrRestartUncertain, fmt.Errorf("发送服务启动控制失败：%w", err))
	}
	if err := wait("running"); err != nil {
		return errors.Join(ErrRestartUncertain, fmt.Errorf("核对服务运行失败：%w", err))
	}
	return nil
}
