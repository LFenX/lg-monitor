//go:build windows && integration

package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/transport"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/eventlog"
)

func TestNativeEventLogOutboxResumesEventsWrittenWhileCollectorStopped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	provider := fmt.Sprintf("lgmon-fake-cache-%d", time.Now().UnixNano())
	assertUnregistered := func() {
		t.Helper()
		key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\EventLog\Application\`+provider, registry.READ)
		if err == nil {
			key.Close()
			t.Fatal("test registered its event source")
		}
		if err != registry.ErrNotExist {
			t.Fatalf("source registration: %v", err)
		}
	}
	assertUnregistered()
	defer assertUnregistered()
	writer, err := eventlog.Open(provider)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	options := transport.OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: "fake-native", Now: time.Now}
	box, err := transport.OpenOutbox(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { box.Close() }()
	cfg := &config.Config{Targets: []config.Target{{ID: "native-owned", EventLog: &config.EventLogSubscription{Channel: "Application", Provider: provider, IDs: []int{6061}}}}}
	start := func() (context.CancelFunc, chan error) {
		feed := eventLogFeed{values: make(chan *config.Config, 1)}
		feed.values <- cfg
		c := &Collector{options: CollectorOptions{Config: feed, Sink: box, EventLogs: collector.NewEventLogSource(nil), Clock: collector.NewProbeClock(), Now: time.Now, Logger: slog.Default()}}
		emitter, err := pipeline.NewEmitter(options.CollectorID, box, time.Now)
		if err != nil {
			t.Fatal(err)
		}
		work, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- c.runEventLogChannel(work, emitter, "Application", &eventLogLimit{}) }()
		t.Cleanup(stop)
		return stop, done
	}
	wait := func(predicate func() bool) {
		t.Helper()
		timer := time.NewTicker(10 * time.Millisecond)
		defer timer.Stop()
		for !predicate() {
			select {
			case <-ctx.Done():
				t.Fatal("owned native event did not reach the durable cache")
			case <-timer.C:
			}
		}
	}
	owned := func() []pipeline.Message {
		batch, err := box.NextBatch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result := []pipeline.Message{}
		for _, row := range batch {
			if row.EventLog != nil && row.EventLog.Provider == provider {
				result = append(result, row)
			}
		}
		return result
	}
	stop, done := start()
	wait(func() bool {
		state, exists, err := box.EventLogCheckpointState(ctx, "Application")
		if err != nil {
			t.Fatal(err)
		}
		_, ready := state.Fences["native-owned"]
		return exists && ready
	})
	if err := writer.Info(6061, "owned before restart"); err != nil {
		t.Fatal(err)
	}
	wait(func() bool { return len(owned()) == 1 })
	stop()
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	if err := box.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Info(6061, "owned while collector stopped"); err != nil {
		t.Fatal(err)
	}
	box, err = transport.OpenOutbox(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	stop, done = start()
	wait(func() bool { return len(owned()) >= 2 })
	stop()
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	rows := owned()
	if len(rows) != 2 || rows[0].EventLog.Message != "owned before restart" || rows[1].EventLog.Message != "owned while collector stopped" || rows[0].EventLog.RecordID >= rows[1].EventLog.RecordID || rows[0].Seq >= rows[1].Seq {
		t.Fatalf("native restart lost or duplicated owned events: %+v", rows)
	}
}
