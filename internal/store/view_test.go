package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func TestReadOnlyViewWhileRunningAndAfterStop(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "中文 # %", "lgmon.db")
	if _, err := OpenView(ctx, path); !errors.Is(err, ErrNoData) {
		t.Fatalf("missing view: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("opening a missing read-only database created a directory")
	}
	writable, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer writable.Close()
	if err := writable.Write(ctx, pipeline.Message{CollectorID: "fake", Seq: 1, TS: 1000, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "demo", State: "down", Reason: "虚构原因"}}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"firing", "resolved"} {
		alert := rules.Alert{ID: "fake-" + state, RuleID: "rule-" + state, CollectorID: "fake", TargetID: "demo", State: state, Severity: "critical", StartedAt: 1000}
		if state == "resolved" {
			alert.ResolvedAt = 2000
		}
		if err := writable.SaveTransition(ctx, alert, rules.Event{AlertID: alert.ID, TS: 2000, Kind: state}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writable.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	for _, stopped := range []bool{false, true} {
		if stopped {
			if err := writable.Close(); err != nil {
				t.Fatal(err)
			}
		}
		view, err := OpenView(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		targets, targetErr := view.CurrentTargets(ctx)
		active, activeErr := view.Alerts(ctx, false)
		all, allErr := view.Alerts(ctx, true)
		if targetErr != nil || activeErr != nil || allErr != nil || len(targets) != 1 || targets[0].State != "down" || len(active) != 1 || active[0].State != "firing" || len(all) != 2 {
			t.Fatalf("view stopped=%v: targets=%+v active=%+v all=%+v errors=%v/%v/%v", stopped, targets, active, all, targetErr, activeErr, allErr)
		}
		if _, err := view.db.ExecContext(ctx, `DELETE FROM alerts`); err == nil {
			t.Fatal("read-only view allowed a write")
		}
		if err := view.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
