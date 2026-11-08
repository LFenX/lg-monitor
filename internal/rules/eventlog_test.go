package rules

import (
	"context"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestEventLogRuleWindowUsesClockCorrectionAndKeepsOriginalStartTime(t *testing.T) {
	for _, offset := range []time.Duration{5 * time.Minute, -5 * time.Minute} {
		t.Run(offset.String(), func(t *testing.T) {
			now := time.UnixMilli(2000000000000)
			sourceAt := now.Add(-offset - time.Second)
			record := collector.EventLogRecord{Channel: "Application", Provider: "Application Error", EventID: 1000, RecordID: 1, TS: sourceAt.UnixMilli(), Presets: []string{"app_crash"}}
			message := pipeline.Message{CollectorID: "host-test", Seq: 1, TS: record.TS, Kind: pipeline.KindEventLog, EventLog: &record}
			cfg := &config.Config{Rules: []config.Rule{{ID: "crash", Type: "eventlog", Preset: "app_crash", Window: "10s"}}}
			repository := &memoryRepository{alerts: map[string]Alert{}}
			queue := &notificationQueue{alerts: make(chan Alert, 5)}
			engine, err := New(context.Background(), Options{Config: cfg, Repository: repository, Notifications: queue, Now: func() time.Time { return now }, Collectors: clockPresence{offset: offset.Milliseconds(), now: func() time.Time { return now }}, EventLogs: replayEventLogQueries{messages: []pipeline.Message{message}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := engine.Handle(context.Background(), message); err != nil {
				t.Fatal(err)
			}
			alert := takeAlert(t, queue, StateFiring)
			if alert.RuleID != "crash" || alert.StartedAt != sourceAt.UnixMilli() {
				t.Fatalf("clock correction rewrote source start: %+v", alert)
			}
			now = now.Add(9 * time.Second)
			if err := engine.Evaluate(context.Background()); err != nil {
				t.Fatal(err)
			}
			takeAlert(t, queue, StateResolved)
		})
	}
}
