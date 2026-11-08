package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestHTTPAlertEventsReadPermissionWindowPaginationAndPrecision(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	alert := rules.Alert{ID: "fake-down", RuleID: "down", CollectorID: "fake-host", TargetID: "web", State: rules.StateFiring, Severity: "warning", StartedAt: f.now.UnixMilli(), SourceTS: f.now.UnixMilli()}
	save := func(alert rules.Alert) {
		t.Helper()
		if err := f.db.SaveTransition(ctx, alert, rules.Event{AlertID: alert.ID, TS: alert.StartedAt, Kind: alert.State, Message: "fake down"}); err != nil {
			t.Fatal(err)
		}
	}
	save(alert)
	for index := 0; index < 8; index++ {
		ts := alert.SourceTS - 60000 + int64(index)*20000
		event := collector.EventLogRecord{Channel: "Application", Provider: "Application Error", EventID: 1000, Level: 2, RecordID: 9007199254740993 + uint64(index), TS: ts, Subject: "fake.exe", Message: fmt.Sprintf("event %d", index), Presets: []string{"app_crash"}, Targets: []collector.EventLogTarget{{ID: "web", Via: "subject"}}}
		if index == 2 {
			event.Targets[0].Via = "subscription"
		}
		if index == 3 {
			event.Targets[0].Via = "both"
		}
		if err := f.db.Write(ctx, pipeline.Message{CollectorID: "fake-host", Seq: uint64(index + 1), TS: ts, Kind: pipeline.KindEventLog, EventLog: &event}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", "/api/v1/query", "read", `{"sql":"SELECT target_id,via,event_id FROM v_eventlog_targets WHERE target_id='web' LIMIT 1"}`, 200)
	var first store.EventLogPage
	response := f.request(t, "GET", "/api/v1/alerts/fake-down/events?limit=2", "read", "", 200)
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil || len(first.Events) != 2 || first.NextCursor == nil || first.From == nil || *first.From != alert.SourceTS-60000 || first.Events[0].RecordID != "9007199254740993" {
		t.Fatalf("first page: %s %v", response.Body, err)
	}
	var second store.EventLogPage
	response = f.request(t, "GET", "/api/v1/alerts/fake-down/events?limit=2&cursor="+url.QueryEscape(*first.NextCursor), "read", "", 200)
	if err := json.Unmarshal(response.Body.Bytes(), &second); err != nil || len(second.Events) != 2 || second.Events[0].Message != "event 3" || second.NextCursor == nil {
		t.Fatalf("subject-only continuation: %s %v", response.Body, err)
	}
	response = f.request(t, "GET", "/api/v1/alerts/fake-down/events?limit=2&cursor="+url.QueryEscape(*second.NextCursor), "read", "", 200)
	if err := json.Unmarshal(response.Body.Bytes(), &second); err != nil || len(second.Events) != 2 || second.Events[1].Message != "event 6" || second.NextCursor != nil {
		t.Fatalf("last page: %s %v", response.Body, err)
	}
	other := alert
	other.ID = "fake-other"
	other.RuleID = "other"
	save(other)
	f.request(t, "GET", "/api/v1/alerts/fake-other/events?cursor="+url.QueryEscape(*first.NextCursor), "read", "", 400)
	for _, query := range []string{"limit=0", "limit=501", "limit=bad", "cursor=garbage"} {
		f.request(t, "GET", "/api/v1/alerts/fake-down/events?"+query, "read", "", 400)
	}
	f.request(t, "GET", "/api/v1/alerts/missing/events", "read", "", 404)
	legacy := alert
	legacy.ID = "legacy"
	legacy.RuleID = "legacy"
	legacy.SourceTS = 0
	save(legacy)
	response = f.request(t, "GET", "/api/v1/alerts/legacy/events", "read", "", 200)
	if !strings.Contains(response.Body.String(), `"events":[]`) || !strings.Contains(response.Body.String(), `"from":null`) || !strings.Contains(response.Body.String(), `"nextCursor":null`) {
		t.Fatalf("legacy empty window: %s", response.Body)
	}
	response = f.request(t, "GET", "/api/v1/openapi.json", "read", "", 200)
	if !strings.Contains(response.Body.String(), "/api/v1/alerts/{id}/events") {
		t.Fatal("event reader missing from OpenAPI")
	}
}
