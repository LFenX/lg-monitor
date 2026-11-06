package winsvc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

const Name = "lgmon"

var ErrNotInstalled = errors.New("lgmon 服务尚未安装")

type State string

const (
	Stopped      State = "stopped"
	StartPending State = "start_pending"
	Running      State = "running"
	StopPending  State = "stop_pending"
)

type Status struct {
	Installed bool   `json:"installed"`
	State     State  `json:"state"`
	PID       uint32 `json:"pid"`
}

type Definition struct {
	Executable, Home, Role, Listen, Monitor, LogLevel string
}

func (d Definition) Arguments() []string {
	args := []string{"run", "--home", d.Home, "--role", d.Role, "--log-level", d.LogLevel}
	if d.Listen != "" {
		args = append(args, "--listen", d.Listen)
	}
	if d.Monitor != "" {
		args = append(args, "--monitor", d.Monitor)
	}
	return args
}

type Service interface {
	Status() (Status, error)
	Start() error
	Stop() error
	Recovery(time.Duration) error
	Delete() error
	Close() error
}

type Manager interface {
	Open() (Service, error)
	Create(Definition) (Service, error)
	Close() error
}

type Driver interface {
	Connect(context.Context, bool) (Manager, error)
	Administrator() (bool, error)
}

type EventSource interface {
	Install() error
	Remove() error
}

type Waiter interface {
	Wait(context.Context, time.Duration) error
}

type Options struct {
	Driver  Driver
	Events  EventSource
	Waiter  Waiter
	Now     func() time.Time
	Timeout time.Duration
}

type Controller struct{ options Options }

type timerWaiter struct{}

func (timerWaiter) Wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func NewController(options Options) (*Controller, error) {
	if options.Driver == nil || options.Events == nil {
		return nil, fmt.Errorf("服务管理需要 SCM 与事件来源接口")
	}
	if options.Waiter == nil {
		options.Waiter = timerWaiter{}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Timeout == 0 {
		options.Timeout = 35 * time.Second
	}
	if options.Timeout < 0 {
		return nil, fmt.Errorf("服务等待时限必须为正数")
	}
	return &Controller{options}, nil
}

func (c *Controller) connect(ctx context.Context, write bool) (Manager, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("服务操作已取消：%w", err)
	}
	if write {
		admin, err := c.options.Driver.Administrator()
		if err != nil {
			return nil, fmt.Errorf("检查管理员权限失败：%w", err)
		}
		if !admin {
			return nil, fmt.Errorf("请用管理员身份运行终端，再执行此服务管理命令")
		}
	}
	manager, err := c.options.Driver.Connect(ctx, write)
	if err != nil {
		return nil, fmt.Errorf("连接服务管理器失败：%w", err)
	}
	return manager, nil
}

func (c *Controller) Install(ctx context.Context, definition Definition) error {
	if definition.Role != "collector" && definition.Role != "monitor" && definition.Role != "both" {
		return fmt.Errorf("服务角色必须为 collector、monitor 或 both")
	}
	if !filepath.IsAbs(definition.Executable) || !filepath.IsAbs(definition.Home) {
		return fmt.Errorf("服务程序和数据目录必须使用绝对路径")
	}
	if definition.LogLevel == "" {
		definition.LogLevel = "info"
	}
	manager, err := c.connect(ctx, true)
	if err != nil {
		return err
	}
	defer manager.Close()
	existing, err := manager.Open()
	if err == nil {
		existing.Close()
		return fmt.Errorf("lgmon 服务已经存在，请先查看 service status；不会覆盖已有服务")
	}
	if !errors.Is(err, ErrNotInstalled) {
		return fmt.Errorf("检查已有服务失败：%w", err)
	}
	service, err := manager.Create(definition)
	if err != nil {
		return fmt.Errorf("注册 lgmon 服务失败：%w", err)
	}
	defer service.Close()
	rollback := func(cause error) error {
		if err := service.Delete(); err != nil {
			return fmt.Errorf("服务安装失败且回滚失败，请运行 service status 检查：%w", errors.Join(cause, err))
		}
		return cause
	}
	if err := service.Recovery(10 * time.Second); err != nil {
		return rollback(fmt.Errorf("设置服务失败重启策略失败：%w", err))
	}
	if err := c.options.Events.Install(); err != nil {
		return rollback(fmt.Errorf("注册 lgmon 事件日志来源失败：%w", err))
	}
	return nil
}

func (c *Controller) Status(ctx context.Context) (Status, error) {
	manager, err := c.connect(ctx, false)
	if err != nil {
		return Status{}, err
	}
	defer manager.Close()
	service, err := manager.Open()
	if errors.Is(err, ErrNotInstalled) {
		return Status{State: "not_installed"}, nil
	}
	if err != nil {
		return Status{}, fmt.Errorf("打开 lgmon 服务失败：%w", err)
	}
	defer service.Close()
	status, err := service.Status()
	if err != nil {
		return status, fmt.Errorf("查询 lgmon 服务失败：%w", err)
	}
	status.Installed = true
	return status, nil
}

func (c *Controller) wait(ctx context.Context, service Service, expected State) error {
	started := c.options.Now()
	for {
		status, err := service.Status()
		if err != nil {
			return fmt.Errorf("查询服务转换状态失败：%w", err)
		}
		if status.State == expected {
			return nil
		}
		if expected == Running && status.State == Stopped {
			return fmt.Errorf("服务启动后已停止，请检查 home/logs 与 Windows 应用事件日志")
		}
		if c.options.Now().Sub(started) >= c.options.Timeout {
			return fmt.Errorf("等待服务状态 %s 超过 %s，请运行 service status 检查", expected, c.options.Timeout)
		}
		if err := c.options.Waiter.Wait(ctx, 200*time.Millisecond); err != nil {
			return fmt.Errorf("等待服务状态已取消：%w", err)
		}
	}
}

func (c *Controller) change(ctx context.Context, action string) error {
	manager, err := c.connect(ctx, true)
	if err != nil {
		return err
	}
	defer manager.Close()
	service, err := manager.Open()
	if err != nil {
		return fmt.Errorf("打开 lgmon 服务失败：%w", err)
	}
	defer service.Close()
	current, err := service.Status()
	if err != nil {
		return fmt.Errorf("查询 lgmon 服务失败：%w", err)
	}
	if action == "start" {
		if current.State == Running {
			return nil
		}
		if current.State != StartPending {
			if err := service.Start(); err != nil {
				return fmt.Errorf("启动 lgmon 服务失败：%w", err)
			}
		}
		return c.wait(ctx, service, Running)
	}
	if current.State != Stopped {
		if current.State != StopPending {
			if err := service.Stop(); err != nil {
				return fmt.Errorf("停止 lgmon 服务失败：%w", err)
			}
		}
		if err := c.wait(ctx, service, Stopped); err != nil {
			return err
		}
	}
	if action == "uninstall" {
		if err := service.Delete(); err != nil {
			return fmt.Errorf("删除 lgmon 服务失败：%w", err)
		}
		if err := c.options.Events.Remove(); err != nil {
			return fmt.Errorf("服务已删除，清理 lgmon 事件来源失败：%w", err)
		}
	}
	return nil
}

func (c *Controller) Start(ctx context.Context) error     { return c.change(ctx, "start") }
func (c *Controller) Stop(ctx context.Context) error      { return c.change(ctx, "stop") }
func (c *Controller) Uninstall(ctx context.Context) error { return c.change(ctx, "uninstall") }
