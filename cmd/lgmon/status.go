package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/spf13/cobra"
)

type statusReader interface {
	CurrentTargets(context.Context) ([]store.TargetRecord, error)
	Alerts(context.Context, bool) ([]rules.Alert, error)
	Close() error
}

type statusFactory func(context.Context, string) (statusReader, error)

func openStatus(ctx context.Context, home string) (statusReader, error) {
	return store.OpenView(ctx, filepath.Join(home, "data", "lgmon.db"))
}

type statusResult struct {
	Available  bool                 `json:"available"`
	Targets    []store.TargetRecord `json:"targets"`
	Alerts     []rules.Alert        `json:"alerts"`
	RuleErrors []rules.RuleError    `json:"ruleErrors"`
}

func cliTime(ms int64) string {
	if ms <= 0 {
		return "—"
	}
	return time.UnixMilli(ms).In(time.Local).Format("2006-01-02 15:04:05")
}

func newStatusCommand(open statusFactory) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "status", Short: "读取已保存的目标状态和触发中告警", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, _ := cmd.Flags().GetString("home")
			result := statusResult{Targets: []store.TargetRecord{}, Alerts: []rules.Alert{}, RuleErrors: []rules.RuleError{}}
			view, err := open(cmd.Context(), home)
			if err != nil && !errors.Is(err, store.ErrNoData) {
				return err
			}
			if err == nil {
				defer view.Close()
				result.Available = true
				result.Targets, err = view.CurrentTargets(cmd.Context())
				if err != nil {
					return err
				}
				result.Alerts, err = view.Alerts(cmd.Context(), false)
				if err != nil {
					return err
				}
				if errorsView, ok := view.(interface {
					RecentRuleErrors(context.Context) ([]rules.RuleError, error)
				}); ok {
					result.RuleErrors, err = errorsView.RecentRuleErrors(cmd.Context())
					if err != nil {
						return err
					}
				}
			}
			if jsonOutput {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
					return fmt.Errorf("输出状态 JSON 失败：%w", err)
				}
				return nil
			}
			if !result.Available {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), store.ErrNoData.Error())
				return err
			}
			for _, target := range result.Targets {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", target.CollectorID, target.TargetID, target.State, cliTime(target.TS)); err != nil {
					return fmt.Errorf("输出目标状态失败：%w", err)
				}
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "触发中告警：%d 条\n", len(result.Alerts)); err != nil {
				return fmt.Errorf("输出告警数量失败：%w", err)
			}
			for _, alert := range result.Alerts {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", alert.ID, alert.Severity, alert.TargetID, alert.Summary); err != nil {
					return fmt.Errorf("输出触发中告警失败：%w", err)
				}
			}
			for _, failure := range result.RuleErrors {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "规则 %s 最近错误（%s，连续 %d 次）：%s\n", failure.RuleID, cliTime(failure.At), failure.Consecutive, failure.Error); err != nil {
					return fmt.Errorf("输出规则错误失败：%w", err)
				}
			}
			return nil
		}}
	command.Flags().BoolVar(&jsonOutput, "json", false, "以 JSON 输出数据库中的最近状态")
	return command
}

func newAlertsCommand(open statusFactory) *cobra.Command {
	var jsonOutput, all bool
	command := &cobra.Command{Use: "alerts", Short: "列出告警，默认只列触发中告警", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, _ := cmd.Flags().GetString("home")
			result := struct {
				Available bool          `json:"available"`
				Alerts    []rules.Alert `json:"alerts"`
			}{Alerts: []rules.Alert{}}
			view, err := open(cmd.Context(), home)
			if err != nil && !errors.Is(err, store.ErrNoData) {
				return err
			}
			if err == nil {
				defer view.Close()
				result.Available = true
				result.Alerts, err = view.Alerts(cmd.Context(), all)
				if err != nil {
					return err
				}
			}
			if jsonOutput {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
					return fmt.Errorf("输出告警 JSON 失败：%w", err)
				}
				return nil
			}
			if !result.Available {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), store.ErrNoData.Error())
				return err
			}
			for _, alert := range result.Alerts {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", alert.ID, alert.State, alert.Severity, alert.CollectorID, alert.TargetID, cliTime(alert.StartedAt), alert.Summary); err != nil {
					return fmt.Errorf("输出告警失败：%w", err)
				}
			}
			return nil
		}}
	command.Flags().BoolVar(&jsonOutput, "json", false, "以 JSON 输出告警")
	command.Flags().BoolVar(&all, "all", false, "同时列出已恢复的告警")
	return command
}
