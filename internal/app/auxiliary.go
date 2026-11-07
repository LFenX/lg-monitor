package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func (c *Collector) runAuxiliary(parent context.Context, emitter *pipeline.Emitter) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	opts := c.options
	var workers sync.WaitGroup
	failures := make(chan error, 1)
	start := func(interval time.Duration, capture func(context.Context) error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ticker := opts.Clock.NewTicker(interval)
			defer ticker.Stop()
			for {
				if ctx.Err() != nil {
					return
				}
				if err := capture(ctx); err != nil {
					if ctx.Err() == nil {
						select {
						case failures <- err:
						default:
						}
						cancel()
					}
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.Ticks():
				}
			}
		}()
	}
	if opts.Metrics != nil {
		start(opts.SystemInterval, func(ctx context.Context) error {
			metrics, err := opts.Metrics.Snapshot(ctx)
			if err == nil {
				err = metrics.Validate()
			}
			if err != nil {
				if ctx.Err() == nil {
					opts.Logger.Warn("System metrics collection failed; retrying next cycle")
				}
				return nil
			}
			return emitter.Emit(ctx, pipeline.Message{Kind: pipeline.KindSystemMetrics, TS: metrics.TS, SystemMetrics: &metrics})
		})
	}
	if opts.Services != nil {
		previous := make(map[string]collector.Service)
		start(opts.ServiceInterval, func(ctx context.Context) error {
			services, err := opts.Services.Snapshot(ctx)
			if err != nil {
				if ctx.Err() == nil {
					opts.Logger.Warn("Service enumeration failed; retaining previous snapshot")
				}
				return nil
			}
			next := make(map[string]collector.Service, len(services))
			for _, service := range services {
				if err := service.Validate(); err != nil {
					opts.Logger.Warn("Invalid service snapshot; retaining previous state")
					return nil
				}
				key := strings.ToLower(service.Name)
				if _, exists := next[key]; exists {
					opts.Logger.Warn("Duplicate service snapshot; retaining previous state")
					return nil
				}
				next[key] = service
			}
			keys := make([]string, 0, len(next)+len(previous))
			for key := range next {
				keys = append(keys, key)
			}
			for key := range previous {
				if _, exists := next[key]; !exists {
					keys = append(keys, key)
				}
			}
			slices.Sort(keys)
			for _, key := range keys {
				service, exists := next[key]
				if !exists {
					service = previous[key]
					service.State, service.PID, service.Error = collector.ServiceDeleted, 0, "服务已删除"
				}
				if old, existed := previous[key]; exists && existed && old == service {
					continue
				}
				if err := emitter.Emit(ctx, pipeline.Message{Kind: pipeline.KindServiceStatus, ServiceStatus: &service}); err != nil {
					return fmt.Errorf("上报服务变化失败：%w", err)
				}
			}
			if opts.Snapshots != nil {
				opts.Snapshots.UpdateServices(services)
			}
			previous = next
			return nil
		})
	}
	workers.Wait()
	select {
	case err := <-failures:
		return err
	default:
		return nil
	}
}
