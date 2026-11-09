package app

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
)

func TestSharedCollectionCoalescesEightConsumersAndCopiesResults(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	source := &sharedCollection[[]collector.Process]{now: func() time.Time { return at }, clone: slices.Clone[[]collector.Process], read: func(context.Context) ([]collector.Process, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return []collector.Process{{PID: 42, CreatedAt: 10, CPUPercent: 2}}, nil
	}}
	results := make(chan []collector.Process, 8)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			rows, err := source.snapshot(ctx)
			if err != nil {
				t.Error(err)
			}
			results <- rows
		})
	}
	<-entered
	close(release)
	group.Wait()
	close(results)
	if calls.Load() != 1 {
		t.Fatalf("duplicated native rate sampling: %d", calls.Load())
	}
	for rows := range results {
		if rows[0].PID != 42 || rows[0].CPUPercent != 2 {
			t.Fatal(rows)
		}
		rows[0].PID = 99
	}
	rows, err := source.snapshot(ctx)
	if err != nil || rows[0].PID != 42 {
		t.Fatal("consumer mutated shared facts", rows, err)
	}
	at = at.Add(500 * time.Millisecond)
	if _, err := source.snapshot(ctx); err != nil || calls.Load() != 2 {
		t.Fatal("expired native snapshot was reused", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := source.snapshot(cancelled); !errors.Is(err, context.Canceled) || calls.Load() != 2 {
		t.Fatal("cancelled consumer performed native IO", err)
	}
}

type collectionEventSource func(context.Context, collector.ProcessEventFilter, func(collector.ProcessEvent)) error

func (f collectionEventSource) Run(ctx context.Context, filter collector.ProcessEventFilter, notify func(collector.ProcessEvent)) error {
	return f(ctx, filter, notify)
}

func TestSharedCollectionInvalidatesBeforeExitNotifyAndDuringRead(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	var present atomic.Bool
	present.Store(true)
	var reads atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	processes := &sharedCollection[[]collector.Process]{now: func() time.Time { return at }, clone: slices.Clone[[]collector.Process], read: func(context.Context) ([]collector.Process, error) {
		n := reads.Add(1)
		old := present.Load()
		if n == 1 {
			close(entered)
			<-release
		}
		if old {
			return []collector.Process{{PID: 42, CreatedAt: 1}}, nil
		}
		return nil, nil
	}}
	ports := &sharedCollection[[]collector.Port]{}
	sources := &collectorSources{processes: sharedProcessCollection{sharedCollection: processes}, ports: sharedPortCollection{ports}, events: make(map[collectionEvent]bool)}
	done := make(chan struct{})
	go func() { _, _ = processes.snapshot(ctx); close(done) }()
	<-entered
	event := collector.ProcessEvent{Kind: collector.ProcessExited, PID: 42, TS: at.UnixMilli()}
	present.Store(false)
	sources.invalidate(event)
	close(release)
	<-done
	rows, err := processes.snapshot(ctx)
	if err != nil || len(rows) != 0 || reads.Load() != 2 {
		t.Fatal("event arriving during native read was lost", rows, err)
	}
	present.Store(true)
	wrapped := invalidatingProcessEvents{sources: sources, source: collectionEventSource(func(_ context.Context, _ collector.ProcessEventFilter, notify func(collector.ProcessEvent)) error {
		event.Kind = collector.ProcessStarted
		notify(event)
		notify(event)
		return nil
	})}
	if err := wrapped.Run(ctx, collector.ProcessEventFilter{}, func(collector.ProcessEvent) {
		rows, err := processes.snapshot(ctx)
		if err != nil || len(rows) != 1 {
			t.Error("consumer woke before invalidation", rows, err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 3 || ports.changed.Load() != 2 {
		t.Fatalf("duplicate events discarded rate baseline: reads=%d epoch=%d", reads.Load(), ports.changed.Load())
	}
}
