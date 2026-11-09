package app

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
)

type sharedCollection[T any] struct {
	mu      sync.Mutex
	now     func() time.Time
	read    func(context.Context) (T, error)
	clone   func(T) T
	changed atomic.Uint64
	version uint64
	at      time.Time
	value   T
	err     error
}

func (s *sharedCollection[T]) snapshot(ctx context.Context) (T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	version := s.changed.Load()
	if elapsed := s.now().Sub(s.at); s.at.IsZero() || s.version != version || elapsed < 0 || elapsed >= 500*time.Millisecond {
		s.value, s.err = s.read(ctx)
		s.at = s.now()
		s.version = version
	}
	return s.clone(s.value), s.err
}

type sharedProcessCollection struct {
	*sharedCollection[[]collector.Process]
	directories collector.WorkingDirectorySource
}

func (s sharedProcessCollection) Snapshot(ctx context.Context) ([]collector.Process, error) {
	return s.snapshot(ctx)
}
func (s sharedProcessCollection) WorkingDirectory(ctx context.Context, id collector.ProcessIdentity) (string, error) {
	return s.directories.WorkingDirectory(ctx, id)
}

type sharedPortCollection struct {
	*sharedCollection[[]collector.Port]
}

func (s sharedPortCollection) ListeningTCP(ctx context.Context) ([]collector.Port, error) {
	return s.snapshot(ctx)
}

type sharedMetricsCollection struct {
	*sharedCollection[collector.SystemMetrics]
}

func (s sharedMetricsCollection) Snapshot(ctx context.Context) (collector.SystemMetrics, error) {
	return s.snapshot(ctx)
}

type sharedServiceCollection struct {
	*sharedCollection[[]collector.Service]
	source collector.ServiceSource
}

func (s sharedServiceCollection) Snapshot(ctx context.Context) ([]collector.Service, error) {
	return s.snapshot(ctx)
}
func (s sharedServiceCollection) Lookup(ctx context.Context, names []string) ([]collector.Service, error) {
	return s.source.Lookup(ctx, names)
}

type sharedInventoryCollection struct {
	*sharedCollection[[]collector.InventorySnapshot]
}

func (s sharedInventoryCollection) Snapshot(ctx context.Context) ([]collector.InventorySnapshot, error) {
	return s.snapshot(ctx)
}

type collectorSources struct {
	processes sharedProcessCollection
	ports     sharedPortCollection
	metrics   sharedMetricsCollection
	services  sharedServiceCollection
	inventory sharedInventoryCollection
	mu        sync.Mutex
	events    map[collectionEvent]bool
}
type collectionEvent struct {
	kind collector.ProcessEventKind
	pid  int32
	ts   int64
}

func newCollectorSources(now func() time.Time, logger *slog.Logger, inventory collector.InventorySource) *collectorSources {
	processes := collector.NewProcessSource(now, logger)
	ports := collector.NewPortSource(now, logger)
	metrics := collector.NewMetricsSource(now, logger)
	services := collector.NewServiceSource(now, logger)
	directories, _ := processes.(collector.WorkingDirectorySource)
	return &collectorSources{
		processes: sharedProcessCollection{sharedCollection: &sharedCollection[[]collector.Process]{now: now, read: processes.Snapshot, clone: slices.Clone[[]collector.Process]}, directories: directories},
		ports:     sharedPortCollection{&sharedCollection[[]collector.Port]{now: now, read: ports.ListeningTCP, clone: slices.Clone[[]collector.Port]}},
		metrics: sharedMetricsCollection{&sharedCollection[collector.SystemMetrics]{now: now, read: metrics.Snapshot, clone: func(value collector.SystemMetrics) collector.SystemMetrics {
			value.Disks = slices.Clone(value.Disks)
			value.Networks = slices.Clone(value.Networks)
			value.Errors = slices.Clone(value.Errors)
			return value
		}}},
		services: sharedServiceCollection{sharedCollection: &sharedCollection[[]collector.Service]{now: now, read: services.Snapshot, clone: slices.Clone[[]collector.Service]}, source: services},
		inventory: sharedInventoryCollection{&sharedCollection[[]collector.InventorySnapshot]{now: now, read: inventory.Snapshot, clone: func(value []collector.InventorySnapshot) []collector.InventorySnapshot {
			value = slices.Clone(value)
			for i := range value {
				value[i].Entries = slices.Clone(value[i].Entries)
			}
			return value
		}}},
		events: make(map[collectionEvent]bool),
	}
}

// An event must invalidate before waking any consumer, or a cached pre-exit snapshot could hide the exit until the next interval.
func (s *collectorSources) invalidate(event collector.ProcessEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := collectionEvent{event.Kind, event.PID, event.TS}
	if event.TS > 0 && event.Kind != collector.ProcessEventsReady && s.events[key] {
		return
	}
	if len(s.events) >= 256 {
		clear(s.events)
	}
	s.events[key] = true
	s.processes.changed.Add(1)
	s.ports.changed.Add(1)
}

type invalidatingProcessEvents struct {
	source  collector.ProcessEventSource
	sources *collectorSources
}

func (s invalidatingProcessEvents) Run(ctx context.Context, filter collector.ProcessEventFilter, notify func(collector.ProcessEvent)) error {
	return s.source.Run(ctx, filter, func(event collector.ProcessEvent) { s.sources.invalidate(event); notify(event) })
}
