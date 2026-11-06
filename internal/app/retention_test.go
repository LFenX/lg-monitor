package app

import (
	"context"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
)

type observedRetention struct {
	Persistence
	applied chan config.Retention
}

func (s *observedRetention) SetRetention(value config.Retention) error {
	if err := s.Persistence.(retentionStore).SetRetention(value); err != nil {
		return err
	}
	s.applied <- value
	return nil
}

func TestRuntimeAppliesInitialAndReloadedRetention(t *testing.T) {
	f := newRuntimeFixture(t)
	observed := &observedRetention{Persistence: f.db, applied: make(chan config.Retention, 3)}
	f.opts.Store = observed
	runtime, err := NewRuntime(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	if initial := receiveRuntime(t, observed.applied); initial != (config.Retention{}) {
		t.Fatalf("initial retention: %+v", initial)
	}
	receiveRuntime(t, f.source.requests).reply <- snapshotReply{rows: []collector.Process{{PID: 10, Name: "demo.exe", CreatedAt: 1}}}
	manager := f.opts.Config.(*config.Manager)
	updated := manager.Snapshot()
	updated.Settings.Retention = config.Retention{Raw: "48h", Minute: "720h", Hour: "17520h"}
	if err := manager.Apply(updated); err != nil {
		t.Fatal(err)
	}
	if got := receiveRuntime(t, observed.applied); got != updated.Settings.Retention {
		t.Fatalf("runtime kept stale retention: %+v", got)
	}
	updated.Settings.Retention.Raw = "0s"
	if err := manager.Apply(updated); err == nil {
		t.Fatal("invalid policy was published")
	}
	cancel()
	if err := receiveRuntime(t, done); err != nil {
		t.Fatal(err)
	}
}
