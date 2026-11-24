// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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
	if opts.EventLogs != nil {
		workers.Add(1)
		// Event logs are an extra signal: a channel that cannot start must not take down the process,
		// port, probe and service samples running beside it, so it backs off and retries on its own.
		go func() {
			defer workers.Done()
			attempts := 0
			for {
				startedAt := opts.Now()
				err := c.runEventLogs(ctx, emitter)
				if ctx.Err() != nil || err == nil {
					return
				}
				if opts.Now().Sub(startedAt) >= eventLogHealthy {
					attempts = 0
				}
				attempts++
				delay := eventLogRetryDelay(attempts)
				if attempts == 1 || attempts%10 == 0 {
					opts.Logger.Warn("Event log collection stopped; other collectors keep running", "error", err, "failures", attempts, "retryIn", delay)
				}
				wait := opts.Clock.NewTicker(delay)
				select {
				case <-ctx.Done():
					wait.Stop()
					return
				case <-wait.Ticks():
					wait.Stop()
				}
			}
		}()
	}
	if opts.JobFiles != nil {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := c.runJobFiles(ctx, emitter); err != nil && ctx.Err() == nil {
				select {
				case failures <- err:
				default:
				}
				cancel()
			}
		}()
	}
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
		var usageAt time.Time
		start(opts.SystemInterval, func(ctx context.Context) error {
			metrics, err := opts.Metrics.Snapshot(ctx)
			// What each program and container uses changes slowly and costs more to read and keep, so it rides
			// along with a sample about every half minute rather than every one.
			if err == nil && (usageAt.IsZero() || opts.Now().Sub(usageAt) >= deviceUsageEvery) {
				usageAt = opts.Now()
				addDeviceUsage(ctx, &metrics, opts.Snapshots, opts.Containers, opts.Now())
			}
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
	if opts.Inventory != nil {
		previous := map[string]collector.InventorySnapshot{}
		lastSent := map[string]time.Time{}
		start(opts.InventoryInterval, func(ctx context.Context) error {
			snapshots, err := opts.Inventory.Snapshot(ctx)
			if err != nil {
				if ctx.Err() == nil {
					opts.Logger.Warn("Inventory enumeration failed; retaining previous state")
				}
				snapshots = []collector.InventorySnapshot{{Kind: "tasks", Scope: "enumeration", Entries: []collector.InventoryEntry{}, ErrorCode: "read_failed"}}
			} else {
				snapshots = append(snapshots, collector.InventorySnapshot{Kind: "tasks", Scope: "enumeration", Entries: []collector.InventoryEntry{}})
			}
			at := opts.Now()
			for _, snapshot := range snapshots {
				if err := snapshot.Validate(); err != nil {
					opts.Logger.Warn("Invalid inventory snapshot; retaining previous state")
					continue
				}
				key := snapshot.Kind + ":" + snapshot.Scope
				old, exists := previous[key]
				if exists && reflect.DeepEqual(old, snapshot) && at.Sub(lastSent[key]) < 10*time.Minute {
					continue
				}
				if err := emitter.Emit(ctx, pipeline.Message{Kind: pipeline.KindInventory, TS: at.UnixMilli(), Inventory: &snapshot}); err != nil {
					return fmt.Errorf("上报清单快照失败：%w", err)
				}
				copy := snapshot
				copy.Entries = slices.Clone(snapshot.Entries)
				previous[key], lastSent[key] = copy, at
			}
			return nil
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

const deviceUsageEvery = 30 * time.Second

func addDeviceUsage(ctx context.Context, metrics *collector.SystemMetrics, snapshots *collector.SnapshotCache, containers collector.ContainerSource, now time.Time) {
	if snapshots != nil {
		latest := snapshots.Latest()
		// A process list older than two minutes would describe a machine that may no longer exist.
		if latest.CollectedAt > 0 && now.UnixMilli()-latest.CollectedAt < 2*time.Minute.Milliseconds() {
			metrics.ProcessCount = len(latest.Processes)
			metrics.Top = collector.TopProcesses(latest.Processes)
		}
	}
	if containers == nil {
		return
	}
	list, err := containers.Containers(ctx)
	switch {
	case errors.Is(err, collector.ErrNoContainerEngine):
	case err != nil:
		metrics.ContainerError = truncateText(err.Error(), 300)
	default:
		metrics.Containers = list
	}
}

func truncateText(value string, limit int) string {
	if len([]rune(value)) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}
