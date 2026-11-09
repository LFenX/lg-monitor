package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/notify"
)

type ExportConfigSource interface {
	Snapshot() *config.Config
	Subscribe(context.Context) <-chan *config.Config
}

type OTLPClientFactory func(context.Context, config.OTLPSettings) (OTLPHTTPClient, string, error)

type OTLPExportOptions struct {
	Config             ExportConfigSource
	Source             MetricExportSource
	Secrets            SecretReader
	MonitorID, Version string
	Now                func() time.Time
	Clock              collector.ProbeClock
	Waiter             notify.Waiter
	Jitter             func(time.Duration) time.Duration
	NewClient          OTLPClientFactory
	Logger             *slog.Logger
}

func RunOTLPExports(ctx context.Context, opts OTLPExportOptions) error {
	if opts.Config == nil || opts.Source == nil || opts.Now == nil {
		return fmt.Errorf("OTLP 导出需要配置、只读指标源和时钟")
	}
	if opts.Clock == nil {
		opts.Clock = collector.NewProbeClock()
	}
	if opts.Waiter == nil {
		opts.Waiter = nativeOTLPWaiter{}
	}
	if opts.Jitter == nil {
		opts.Jitter = func(base time.Duration) time.Duration {
			return time.Duration(float64(base) * (0.8 + rand.Float64()*0.4))
		}
	}
	if opts.NewClient == nil {
		opts.NewClient = func(ctx context.Context, s config.OTLPSettings) (OTLPHTTPClient, string, error) {
			return newOTLPHTTPClient(ctx, s, opts.Secrets, opts.Now)
		}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	updates := opts.Config.Subscribe(ctx)
	var active config.OTLPSettings
	var initialized bool
	var stop context.CancelFunc
	var done chan struct{}
	stopCurrent := func() {
		if stop != nil {
			stop()
			stop = nil
		}
		if done != nil {
			<-done
			done = nil
		}
	}
	defer stopCurrent()
	for {
		select {
		case <-ctx.Done():
			return nil
		case cfg, ok := <-updates:
			if !ok {
				return nil
			}
			if cfg == nil {
				continue
			}
			settings := cfg.Settings.Exports.OTLP
			if initialized && settings == active {
				continue
			}
			stopCurrent()
			active, initialized = settings, true
			if !settings.Enabled {
				continue
			}
			if err := (config.ExportSettings{OTLP: settings}).Validate(); err != nil {
				return fmt.Errorf("OTLP 设置未通过校验：%w", err)
			}
			work, cancel := context.WithCancel(ctx)
			stop = cancel
			done = make(chan struct{})
			finished := done
			go func() {
				defer close(finished)
				ticker := opts.Clock.NewTicker(settings.EffectiveInterval())
				defer ticker.Stop()
				for {
					if err := exportOTLPCycle(work, opts, settings); err != nil && work.Err() == nil {
						class, status := "source", 0
						var failure *otlpExportError
						if errors.As(err, &failure) {
							class, status = failure.class, failure.status
						}
						opts.Logger.Warn("OTLP metric export failed", "class", class, "status", status)
					}
					select {
					case <-work.Done():
						return
					case <-ticker.Ticks():
					}
				}
			}()
		}
	}
}

func exportOTLPCycle(parent context.Context, opts OTLPExportOptions, settings config.OTLPSettings) error {
	ctx, cancel := context.WithTimeout(parent, settings.EffectiveTimeout())
	defer cancel()
	client, authorization, err := opts.NewClient(ctx, settings)
	if err != nil {
		if ctx.Err() != nil {
			return &otlpExportError{class: "timeout", cause: ctx.Err()}
		}
		return err
	}
	if client == nil {
		return &otlpExportError{class: "config", cause: fmt.Errorf("OTLP HTTP 客户端为空")}
	}
	defer client.CloseIdleConnections()
	snapshot, err := opts.Source.ExportMetrics(ctx, opts.Config.Snapshot().Targets)
	if err != nil {
		if ctx.Err() != nil {
			return &otlpExportError{class: "timeout", cause: ctx.Err()}
		}
		return fmt.Errorf("读取 OTLP 持久指标失败：%w", err)
	}
	batches, err := encodeOTLP(ctx, snapshot, opts.MonitorID, opts.Version)
	if err != nil {
		if ctx.Err() != nil {
			return &otlpExportError{class: "timeout", cause: ctx.Err()}
		}
		return &otlpExportError{class: "encoding", cause: err}
	}
	for _, batch := range batches {
		rejected, partial, err := sendOTLP(ctx, client, settings.Endpoint, authorization, batch, opts.Now, opts.Waiter, opts.Jitter)
		if err != nil {
			return err
		}
		if partial {
			opts.Logger.Warn("OTLP metric batch partially accepted", "rejected", rejected)
		}
	}
	return nil
}
