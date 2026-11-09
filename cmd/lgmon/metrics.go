package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/spf13/cobra"
)

type metricsReader interface {
	ExportMetrics(context.Context, []config.Target) (store.ExportMetricSnapshot, error)
	Close() error
}
type metricsFactory func(context.Context, string) (metricsReader, error)

func openMetrics(ctx context.Context, home string) (metricsReader, error) {
	return store.OpenView(ctx, filepath.Join(home, "data", "lgmon.db"))
}

func newMetricsCommand(open metricsFactory) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{Use: "metrics", Short: "只读导出当前持久指标，默认 Prometheus 文本；不发送网络请求", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		home, _ := cmd.Flags().GetString("home")
		cfg, err := config.Load(os.DirFS(filepath.Join(home, "config")))
		var snapshot store.ExportMetricSnapshot
		if err == nil {
			var reader metricsReader
			reader, err = open(cmd.Context(), home)
			if err == nil {
				snapshot, err = reader.ExportMetrics(cmd.Context(), cfg.Targets)
				err = errors.Join(err, reader.Close())
			}
		}
		if err == nil {
			err = api.ValidateMetricSnapshot(snapshot)
		}
		if err != nil {
			if asJSON {
				if writeErr := json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"error": map[string]string{"code": "metrics_unavailable", "message": err.Error()}}); writeErr != nil {
					return fmt.Errorf("输出指标错误失败：%w", writeErr)
				}
				return &reportedError{err}
			}
			return fmt.Errorf("读取持久指标失败：%w", err)
		}
		if asJSON {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(snapshot); err != nil {
				return fmt.Errorf("输出指标 JSON 失败：%w", err)
			}
			return nil
		}
		body, err := api.EncodePrometheus(snapshot)
		if err != nil {
			return err
		}
		if _, err := cmd.OutOrStdout().Write(body); err != nil {
			return fmt.Errorf("输出 Prometheus 指标失败：%w", err)
		}
		return nil
	}}
	command.Flags().BoolVar(&asJSON, "json", false, "输出同源结构化快照")
	return command
}
