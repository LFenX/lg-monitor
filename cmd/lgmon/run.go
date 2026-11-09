package main

import (
	"context"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/app"
	"github.com/spf13/cobra"
)

type localRunner func(context.Context, app.LocalOptions) error

func newRunCommand(run localRunner) *cobra.Command {
	var role, listen, monitor string
	var noTray bool
	var asJSON bool
	command := &cobra.Command{Use: "run", Short: "运行采集、存储、规则和通知，Ctrl+C 优雅退出", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if role != "both" && role != "collector" && role != "monitor" {
				return &usageError{fmt.Errorf("--role 必须为 both、collector 或 monitor")}
			}
			home, err := cmd.Flags().GetString("home")
			if err != nil {
				return fmt.Errorf("读取运行根目录失败：%w", err)
			}
			return run(cmd.Context(), app.LocalOptions{Home: home, Version: version, Role: role, Listen: listen, Monitor: monitor, NoTray: noTray, Logger: commandLogger(cmd), Notice: func(message string) error {
				if asJSON {
					return commandEvent(cmd, "notice", message)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), message)
				return err
			}})
		}}
	command.Flags().StringVar(&role, "role", "both", "运行角色：both、collector 或 monitor")
	command.Flags().StringVar(&listen, "listen", ":7443", "monitor 的 gRPC 监听地址")
	command.Flags().StringVar(&monitor, "monitor", "", "collector 连接的监控器地址（默认使用配对时保存的地址）")
	command.Flags().BoolVar(&noTray, "no-tray", false, "前台运行时隐藏托盘，供终端和自动化验证使用")
	command.Flags().BoolVar(&asJSON, "json", false, "逐行输出通知 JSON，日志以 JSON 写到 stderr")
	return command
}
