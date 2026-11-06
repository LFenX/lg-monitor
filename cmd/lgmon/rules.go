package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/spf13/cobra"
)

type replayReader interface {
	rules.ReplaySource
	Close() error
}
type replayFactory func(context.Context, string) (replayReader, error)

func openReplay(ctx context.Context, home string) (replayReader, error) {
	return store.OpenView(ctx, filepath.Join(home, "data", "lgmon.db"))
}

func newRulesCommand(now func() time.Time, open replayFactory) *cobra.Command {
	root := &cobra.Command{Use: "rules", Short: "测试告警规则"}
	var since string
	var draftFile string
	var jsonOutput bool
	test := &cobra.Command{Use: "test [规则ID] [--file draft.yaml]", Short: "用历史采样回放规则，不发送通知", Args: func(cmd *cobra.Command, args []string) error {
		if (draftFile == "" && (len(args) != 1 || args[0] == "")) || (draftFile != "" && len(args) != 0) {
			return &usageError{fmt.Errorf("请选择一个规则 ID，或单独使用 --file 草稿路径")}
		}
		return nil
	}, RunE: func(cmd *cobra.Command, args []string) error {
		duration, err := config.Duration(since).Parse()
		if err != nil || duration <= 0 {
			return &usageError{fmt.Errorf("since 须为正数时长，例如 24h；超过保留时长会截断并说明")}
		}
		home, _ := cmd.Flags().GetString("home")
		cfg, err := config.Load(os.DirFS(filepath.Join(home, "config")))
		if err != nil {
			return err
		}
		var definition config.Rule
		if draftFile != "" {
			absolute, err := filepath.Abs(draftFile)
			if err != nil {
				return fmt.Errorf("解析规则草稿路径失败：%w", err)
			}
			definition, err = config.LoadRuleFile(os.DirFS(filepath.Dir(absolute)), filepath.Base(absolute), cfg)
			if err != nil {
				return err
			}
		} else {
			found := false
			for _, rule := range append(cfg.Rules, config.OfflineRules(cfg)...) {
				if rule.ID == args[0] {
					definition, found = rule, true
					break
				}
			}
			if !found {
				return fmt.Errorf("规则 %s 不存在", args[0])
			}
		}
		source, err := open(cmd.Context(), home)
		if err != nil {
			return err
		}
		defer source.Close()
		until := now()
		result, err := rules.Replay(cmd.Context(), rules.ReplayOptions{Config: cfg, Rule: definition, Since: until.Add(-duration), Until: until, Source: source})
		if err != nil {
			return err
		}
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}
		for _, event := range result.Events {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", cliTime(event.TS), event.State, event.Key, event.Summary); err != nil {
				return fmt.Errorf("输出回放结果失败：%w", err)
			}
		}
		for _, warning := range result.Notes {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), "说明："+warning); err != nil {
				return fmt.Errorf("输出回放说明失败：%w", err)
			}
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "评估 %d 次，触发 %d 次，恢复 %d 次，结束时触发中 %d 条，完成=%t\n", result.Evaluations, result.Totals.Fired, result.Totals.Resolved, result.Totals.FiringAtEnd, result.Complete)
		return err
	}}
	test.Flags().StringVar(&since, "since", "24h", "回放时长，超过原始保留时长会截断并说明")
	test.Flags().StringVar(&draftFile, "file", "", "只写一条规则的 YAML 草稿，与规则 ID 互斥")
	test.Flags().BoolVar(&jsonOutput, "json", false, "输出回放 JSON 和数据完整性说明")
	root.AddCommand(test)
	return root
}
