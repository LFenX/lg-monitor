package app

import (
	"context"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/store"
)

type MetricExportSource interface {
	ExportMetrics(context.Context, []config.Target) (store.ExportMetricSnapshot, error)
}

func (b *APIBackend) metricSnapshot(ctx context.Context) (store.ExportMetricSnapshot, error) {
	if b.options.Metrics == nil {
		return store.ExportMetricSnapshot{}, &api.Fault{Status: 503, Code: "metrics_unavailable", Message: "只读指标数据源尚未就绪"}
	}
	snapshot, err := b.options.Metrics.ExportMetrics(ctx, b.options.Config.Snapshot().Targets)
	if err != nil {
		return store.ExportMetricSnapshot{}, &api.Fault{Status: 503, Code: "metrics_unavailable", Message: "读取持久指标失败，请检查数据库和配置范围", Cause: fmt.Errorf("读取持久指标失败：%w", err)}
	}
	if err := api.ValidateMetricSnapshot(snapshot); err != nil {
		return store.ExportMetricSnapshot{}, &api.Fault{Status: 503, Code: "metrics_unavailable", Message: "指标数据超出协议界限，请检查采样数据和配置范围", Cause: fmt.Errorf("校验指标快照失败：%w", err)}
	}
	return snapshot, nil
}
