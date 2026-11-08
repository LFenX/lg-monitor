//go:build windows && integration && webui

package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func TestNativeEventLogBrowser(t *testing.T) {
	f := newAPIFixtureWithHTTPClock(t, time.Date(2026, 11, 9, 8, 30, 0, 0, time.UTC), time.Now)
	alert := rules.Alert{ID: "fake-event-alert", RuleID: "down", CollectorID: "fake-host", TargetID: "web", State: rules.StateFiring, Severity: "critical", StartedAt: f.now.UnixMilli(), SourceTS: f.now.UnixMilli(), Summary: "虚构事件诊断"}
	if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, TS: alert.StartedAt, Kind: alert.State, Message: alert.Summary}); err != nil {
		t.Fatal(err)
	}
	write := func(ctx context.Context, id uint64, ts int64, message string) error {
		event := collector.EventLogRecord{Channel: "Application", Provider: "Application Error", EventID: 1000, Level: 2, RecordID: 9007199254740993 + id, TS: ts, Subject: "fake-web.exe", Message: message, Presets: []string{"app_crash"}, Targets: []collector.EventLogTarget{{ID: "web", Via: "both"}}}
		return f.db.Write(ctx, pipeline.Message{CollectorID: "fake-host", Seq: id + 1, TS: ts, Kind: pipeline.KindEventLog, EventLog: &event})
	}
	for i := range 205 {
		if err := write(context.Background(), uint64(i), alert.SourceTS-60000+int64(i)*500, "虚构诊断事件"); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/__fixture/eventlog-late" {
			if err := write(r.Context(), 205, alert.SourceTS-59750, "刷新后到达的虚构事件"); err != nil {
				http.Error(w, "fake write failed", 500)
				return
			}
			if err := f.db.Flush(r.Context()); err != nil {
				http.Error(w, "fake flush failed", 500)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		f.server.ServeHTTP(w, r)
	})
	runNativeWebBrowser(t, f, "eventlog.spec.ts", handler)
	if len(f.client.sent) != 0 || f.hookFactories != 0 || len(f.toast.values) != 0 {
		t.Fatal("event viewing attempted notifications")
	}
}
