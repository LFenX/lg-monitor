package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/LFenX/lg-monitor/internal/app"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/winsvc"
	"github.com/spf13/cobra"
)

type serviceController interface {
	Install(context.Context, winsvc.Definition) error
	Uninstall(context.Context) error
	Start(context.Context) error
	Stop(context.Context) error
	Status(context.Context) (winsvc.Status, error)
}

type serviceDependencies struct {
	Controller      func() (serviceController, error)
	Executable      func() (string, error)
	ProxyConfigured func() bool
	Validate        func(string) error
}

func defaultServiceDependencies() serviceDependencies {
	return serviceDependencies{
		Controller: func() (serviceController, error) { return winsvc.NativeController() },
		Executable: os.Executable,
		ProxyConfigured: func() bool {
			return os.Getenv("HTTPS_PROXY") != "" || os.Getenv("HTTP_PROXY") != "" || os.Getenv("ALL_PROXY") != ""
		},
		Validate: func(home string) error { _, err := config.Load(os.DirFS(filepath.Join(home, "config"))); return err },
	}
}

type serviceResult struct {
	Status    string       `json:"status"`
	Message   string       `json:"message"`
	Name      string       `json:"name"`
	Installed *bool        `json:"installed,omitempty"`
	State     winsvc.State `json:"state,omitempty"`
	PID       *uint32      `json:"pid,omitempty"`
	Warnings  []string     `json:"warnings"`
}

func printServiceResult(cmd *cobra.Command, result serviceResult, jsonOutput bool, failure error) error {
	if jsonOutput {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
			return fmt.Errorf("输出服务结果失败：%w", err)
		}
	} else {
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), result.Message); err != nil {
			return fmt.Errorf("输出服务结果失败：%w", err)
		}
		for _, warning := range result.Warnings {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), warning); err != nil {
				return fmt.Errorf("输出服务提示失败：%w", err)
			}
		}
	}
	if failure != nil {
		return &reportedError{failure}
	}
	return nil
}

func newInstallCommand(deps serviceDependencies) *cobra.Command {
	var role, listen, monitor string
	var jsonOutput bool
	command := &cobra.Command{Use: "install", Short: "注册 LocalSystem 自动启动服务，需要管理员权限", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if role != "both" && role != "collector" && role != "monitor" {
			return &usageError{fmt.Errorf("--role 必须为 both、collector 或 monitor")}
		}
		result := serviceResult{Status: "ok", Name: winsvc.Name, Warnings: []string{}}
		home, _ := cmd.Flags().GetString("home")
		home, err := filepath.Abs(home)
		if err != nil {
			return fmt.Errorf("解析服务数据目录失败：%w", err)
		}
		level, _ := cmd.Flags().GetString("log-level")
		if level == "" {
			level = "info"
		}
		if role != "collector" && deps.Validate != nil {
			err = deps.Validate(home)
		}
		var executable string
		if err == nil {
			executable, err = deps.Executable()
		}
		if err == nil {
			executable, err = filepath.Abs(executable)
		}
		if err == nil {
			var controller serviceController
			controller, err = deps.Controller()
			if err == nil {
				err = controller.Install(cmd.Context(), winsvc.Definition{Executable: executable, Home: home, Role: role, Listen: listen, Monitor: monitor, LogLevel: level})
			}
		}
		if err != nil {
			result.Status, result.Message = "error", err.Error()
		} else {
			installed := true
			result.Installed, result.State = &installed, winsvc.Stopped
			result.Message = "lgmon 服务已注册（LocalSystem、开机自动启动、失败后 10 秒重启）；运行 lgmon service start 启动"
			if deps.ProxyConfigured != nil && deps.ProxyConfigured() {
				result.Warnings = append(result.Warnings, "检测到当前用户代理：服务不继承用户环境；飞书接口通常可直连，需要代理时请在 notifiers.yaml 显式填写 proxy 地址")
			}
		}
		return printServiceResult(cmd, result, jsonOutput, err)
	}}
	command.Flags().StringVar(&role, "role", "both", "服务角色：collector、monitor 或 both")
	command.Flags().StringVar(&listen, "listen", ":7443", "监控器 gRPC 监听地址")
	command.Flags().StringVar(&monitor, "monitor", "", "采集器监控地址（默认使用配对时保存的地址）")
	command.Flags().BoolVar(&jsonOutput, "json", false, "以 JSON 输出注册结果")
	return command
}

func serviceActionCommand(use, description string, deps serviceDependencies) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: use, Short: description, Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result := serviceResult{Status: "ok", Name: winsvc.Name, Warnings: []string{}}
		controller, err := deps.Controller()
		if err == nil {
			switch use {
			case "start":
				err = controller.Start(cmd.Context())
			case "stop":
				err = controller.Stop(cmd.Context())
			case "uninstall":
				err = controller.Uninstall(cmd.Context())
			}
		}
		if err == nil {
			if use == "uninstall" {
				installed := false
				result.Installed = &installed
				result.State = "not_installed"
				result.Message = "lgmon 服务已卸载，配置、数据和密钥已保留"
			} else {
				var status winsvc.Status
				status, err = controller.Status(cmd.Context())
				if err == nil {
					result.Installed, result.State, result.PID = &status.Installed, status.State, &status.PID
					result.Message = fmt.Sprintf("lgmon 服务：%s（PID %d）", status.State, status.PID)
				}
			}
		}
		if err != nil {
			result.Status, result.Message = "error", err.Error()
		}
		return printServiceResult(cmd, result, jsonOutput, err)
	}}
	command.Flags().BoolVar(&jsonOutput, "json", false, "以 JSON 输出服务状态")
	return command
}

func newUninstallCommand(deps serviceDependencies) *cobra.Command {
	return serviceActionCommand("uninstall", "停止并卸载 lgmon 服务，保留配置和数据", deps)
}

func newServiceCommand(deps serviceDependencies) *cobra.Command {
	command := &cobra.Command{Use: "service", Short: "管理 lgmon Windows 服务", Args: noArgs}
	command.AddCommand(serviceActionCommand("start", "启动服务并等待运行状态", deps), serviceActionCommand("stop", "停止服务并等待停止状态", deps), serviceActionCommand("status", "只读查询服务状态", deps))
	return command
}

func nativeRun(ctx context.Context, opts app.LocalOptions) error {
	if ctx.Err() != nil {
		return nil
	}
	service, err := winsvc.IsService()
	if err != nil {
		return fmt.Errorf("检查 Windows 服务运行上下文失败：%w", err)
	}
	if !service {
		return app.RunLocal(ctx, opts)
	}
	level := slog.LevelError
	for _, candidate := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		if opts.Logger.Enabled(ctx, candidate) {
			level = candidate
			break
		}
	}
	return winsvc.RunService(ctx, opts.Home, level, func(ctx context.Context, runtime winsvc.Runtime) error {
		opts.Logger, opts.Ready, opts.ServiceIdentity = runtime.Logger, runtime.Ready, runtime.Identity
		opts.Notice = func(string) error {
			runtime.Logger.Warn("Feishu channel is not configured; run lgmon feishu setup")
			return nil
		}
		return app.RunLocal(ctx, opts)
	})
}
