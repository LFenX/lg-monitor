package rules_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

type notificationRecorder struct{ alerts []rules.Alert }

func (r *notificationRecorder) Enqueue(_ context.Context, alert rules.Alert) error {
	r.alerts = append(r.alerts, alert)
	return nil
}

func TestSQLiteRestartKeepsOneIncidentAndSendsOnlyRecovery(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "monitor.db")
	now := func() time.Time { return time.UnixMilli(1000000) }
	open := func() *store.Store {
		t.Helper()
		s, err := store.Open(ctx, store.Options{Path: path, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
		return s
	}
	cfg := &config.Config{Targets: []config.Target{{ID: "web", Match: config.Match{Process: "demo.exe"}}}, Rules: []config.Rule{{ID: "down", Target: "web", Type: "process_absent", For: "0s", Severity: "critical"}}}
	firstStore, firstQueue := open(), &notificationRecorder{}
	first, err := rules.New(ctx, rules.Options{Config: cfg, Repository: firstStore, Notifications: firstQueue, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	status := pipeline.Message{CollectorID: "test-host", Seq: 1, TS: now().UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: collector.StateDown, Process: collector.ProcessMissing}}
	if err := first.Handle(ctx, status); err != nil {
		t.Fatal(err)
	}
	if len(firstQueue.alerts) != 1 {
		t.Fatalf("firing notifications = %d", len(firstQueue.alerts))
	}
	if err := firstStore.Close(); err != nil {
		t.Fatal(err)
	}
	secondStore, secondQueue := open(), &notificationRecorder{}
	second, err := rules.New(ctx, rules.Options{Config: cfg, Repository: secondStore, Notifications: secondQueue, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.Handle(ctx, status); err != nil {
		t.Fatal(err)
	}
	if len(secondQueue.alerts) != 0 {
		t.Fatal("restart repeated the firing notification")
	}
	status.Seq++
	status.TargetStatus.State = collector.StateUp
	status.TargetStatus.Process = collector.ProcessFound
	if err := second.Handle(ctx, status); err != nil {
		t.Fatal(err)
	}
	if len(secondQueue.alerts) != 1 || secondQueue.alerts[0].State != rules.StateResolved || secondQueue.alerts[0].ID != firstQueue.alerts[0].ID {
		t.Fatalf("recovery = %+v", secondQueue.alerts)
	}
	if active, err := secondStore.ActiveAlerts(ctx); err != nil || len(active) != 0 {
		t.Fatalf("active after recovery = %+v, %v", active, err)
	}
}
