package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/spf13/cobra"
)

type pauseControls interface {
	SetNotificationPause(context.Context, time.Duration, string, string) (store.NotificationPause, error)
	ResumeNotifications(context.Context) (store.NotificationPause, error)
	Close() error
}

func openPauseControls(ctx context.Context, home string) (pauseControls, error) {
	db, err := openRuleControls(ctx, home)
	if err != nil {
		return nil, err
	}
	pause, ok := db.(pauseControls)
	if !ok {
		db.Close()
		return nil, fmt.Errorf("通知暂停存储不可用")
	}
	return pause, nil
}

func newNotifyCommand(open func(context.Context, string) (pauseControls, error)) *cobra.Command {
	root := &cobra.Command{Use: "notify", Short: "暂停或恢复全部告警通知，监控继续记录"}
	root.PersistentFlags().Bool("json", false, "输出暂停状态 JSON（默认也输出 JSON）")
	var duration, reason string
	pause := &cobra.Command{Use: "pause", Short: "暂停通知 1 分钟到 24 小时", Args: noArgs}
	resume := &cobra.Command{Use: "resume", Short: "恢复通知，补发仍需关注的告警", Args: noArgs}
	pause.Flags().StringVar(&duration, "for", "", "暂停时长，例如 1h")
	pause.Flags().StringVar(&reason, "reason", "", "暂停原因，最多 200 个字符")
	_ = pause.MarkFlagRequired("for")
	for _, cmd := range []*cobra.Command{pause, resume} {
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			var d time.Duration
			if cmd == pause {
				var err error
				d, err = config.Duration(duration).Parse()
				if err != nil || d < time.Minute || d > 24*time.Hour || len([]rune(reason)) > 200 {
					return &usageError{fmt.Errorf("暂停时长必须为 1 分钟到 24 小时，原因最多 200 个字符")}
				}
			}
			home, _ := cmd.Flags().GetString("home")
			db, err := open(cmd.Context(), home)
			if err != nil {
				return err
			}
			defer db.Close()
			var state store.NotificationPause
			if cmd == pause {
				state, err = db.SetNotificationPause(cmd.Context(), d, reason, "local")
			} else {
				state, err = db.ResumeNotifications(cmd.Context())
			}
			if err != nil {
				return err
			}
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(state); err != nil {
				return fmt.Errorf("输出通知暂停状态失败：%w", err)
			}
			return nil
		}
		root.AddCommand(cmd)
	}
	return root
}
