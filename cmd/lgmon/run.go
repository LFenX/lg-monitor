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
	var role string
	command := &cobra.Command{Use: "run", Short: "运行采集、存储、规则和通知，Ctrl+C 优雅退出", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if role != "both" {
				return &usageError{fmt.Errorf("M0 仅支持 --role both；独立 collector/monitor 角色在 M1 实现")}
			}
			home, err := cmd.Flags().GetString("home")
			if err != nil {
				return fmt.Errorf("读取运行根目录失败：%w", err)
			}
			return run(cmd.Context(), app.LocalOptions{Home: home, Version: version, Logger: slog.Default(), Notice: func(message string) error {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), message)
				return err
			}})
		}}
	command.Flags().StringVar(&role, "role", "both", "运行角色：both")
	return command
}
