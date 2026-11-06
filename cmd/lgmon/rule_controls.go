package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/spf13/cobra"
)

type ruleControls interface {
	AcknowledgeAlert(context.Context, string) error
	AddSilence(context.Context, string, string, time.Duration, string) (store.Silence, error)
	Close() error
}
type ruleControlFactory func(context.Context, string) (ruleControls, error)

func openRuleControls(ctx context.Context, home string) (ruleControls, error) {
	path := filepath.Join(home, "data", "lgmon.db")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, store.ErrNoData
		}
		return nil, fmt.Errorf("读取规则数据库失败：%w", err)
	}
	return store.Open(ctx, store.Options{Path: path})
}

func newAckCommand(open ruleControlFactory) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{Use: "ack <告警ID>", Short: "确认触发中告警，停止重复提醒，恢复时仍通知", Args: func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 || args[0] == "" {
			return &usageError{fmt.Errorf("请指定一个告警 ID")}
		}
		return nil
	}, RunE: func(cmd *cobra.Command, args []string) error {
		home, _ := cmd.Flags().GetString("home")
		controls, err := open(cmd.Context(), home)
		if err != nil {
			return err
		}
		defer controls.Close()
		if err := controls.AcknowledgeAlert(cmd.Context(), args[0]); err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"ok": true, "alertId": args[0]})
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "已确认告警；停止重复提醒，恢复时仍通知")
		return err
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "输出确认结果 JSON")
	return cmd
}

func newSilenceCommand(open ruleControlFactory) *cobra.Command {
	root := &cobra.Command{Use: "silence", Short: "管理告警静音"}
	var ruleID, targetID, duration, reason string
	var jsonOutput bool
	add := &cobra.Command{Use: "add", Short: "按规则或目标静音，状态继续记录", Args: noArgs, RunE: func(cmd *cobra.Command, args []string) error {
		parsed, err := time.ParseDuration(duration)
		if err != nil || parsed <= 0 {
			return &usageError{fmt.Errorf("for 必须为正数合法时长，例如 2h")}
		}
		home, _ := cmd.Flags().GetString("home")
		controls, err := open(cmd.Context(), home)
		if err != nil {
			return err
		}
		defer controls.Close()
		row, err := controls.AddSilence(cmd.Context(), ruleID, targetID, parsed, reason)
		if err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(row)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "已静音 %s，截止 %s；状态继续记录\n", row.ID, cliTime(row.EndsAt))
		return err
	}}
	add.Flags().StringVar(&ruleID, "rule", "", "规则 ID")
	add.Flags().StringVar(&targetID, "target", "", "目标 ID")
	add.Flags().StringVar(&duration, "for", "", "静音时长，例如 2h")
	add.Flags().StringVar(&reason, "reason", "", "静音原因")
	add.Flags().BoolVar(&jsonOutput, "json", false, "输出静音结果 JSON")
	add.MarkFlagsMutuallyExclusive("rule", "target")
	add.MarkFlagsOneRequired("rule", "target")
	_ = add.MarkFlagRequired("for")
	_ = add.MarkFlagRequired("reason")
	root.AddCommand(add)
	return root
}
