package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/LFenX/lg-monitor/internal/app"
	"github.com/spf13/cobra"
)

type localRunner func(context.Context, app.LocalOptions) error

func newRunCommand(run localRunner) *cobra.Command {
	var role, listen, monitor string
	command := &cobra.Command{Use: "run", Short: "运行采集、存储、规则和通知，Ctrl+C 优雅退出", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if role != "both" && role != "collector" && role != "monitor" {
				return &usageError{fmt.Errorf("--role 必须为 both、collector 或 monitor")}
			}
			home, err := cmd.Flags().GetString("home")
			if err != nil {
				return fmt.Errorf("读取运行根目录失败：%w", err)
			}
			return run(cmd.Context(), app.LocalOptions{Home: home, Version: version, Role: role, Listen: listen, Monitor: monitor, Logger: slog.Default(), Notice: func(message string) error {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), message)
				return err
			}})
		}}
	command.Flags().StringVar(&role, "role", "both", "运行角色：both、collector 或 monitor")
	command.Flags().StringVar(&listen, "listen", ":7443", "monitor 的 gRPC 监听地址")
	command.Flags().StringVar(&monitor, "monitor", "127.0.0.1:7443", "collector 连接的监控器地址")
	return command
}
