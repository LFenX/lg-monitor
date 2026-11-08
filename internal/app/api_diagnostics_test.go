package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

func seedAPIDiagnostic(t *testing.T, f *apiFixture) {
	t.Helper()
	f.samples(t)
	ctx := context.Background()
	anchor := f.now.Add(-7 * time.Second).UnixMilli()
	alert := rules.Alert{ID: "fake-diagnostic", RuleID: "down", CollectorID: "fake-host", TargetID: "web", State: rules.StateFiring, Severity: "critical", SourceTS: anchor, StartedAt: anchor, Summary: "fake-private-alert", InstanceKey: "fake-private-instance"}
	if err := f.db.SaveTransition(ctx, alert, rules.Event{AlertID: alert.ID, TS: anchor, Kind: "firing", Message: "fake-private-transition"}); err != nil {
		t.Fatal(err)
	}
	for i, host := range []string{"fake-host", "fake-other"} {
		name := "虚构演示进程.exe"
		if i > 0 {
			name = "fake-other-host.exe"
		}
		for _, message := range []pipeline.Message{
			{CollectorID: host, Seq: 101, TS: anchor - 1000, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: "up", Process: collector.ProcessFound, Processes: []pipeline.ProcessRef{{PID: 42, Name: name, Exe: `C:\fake\demo.exe`, CPUPercent: 1, RSS: 1048576}}}},
			{CollectorID: host, Seq: 102, TS: anchor, Kind: pipeline.KindProbeResult, ProbeResult: &collector.ProbeResult{TargetID: "web", Type: "http", LatencyMS: 30, Error: "fake-private-probe-url"}},
			{CollectorID: host, Seq: 103, TS: anchor, Kind: pipeline.KindEventLog, EventLog: &collector.EventLogRecord{Channel: "Application", Provider: "Application Error", EventID: 1000, Level: 2, RecordID: 9007199254740993, TS: anchor, Subject: name, Message: "fake-private-password-token-log", Targets: []collector.EventLogTarget{{ID: "web", Via: "subject"}}}},
			{CollectorID: host, Seq: 104, TS: f.now.UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: "up", Process: collector.ProcessFound, Processes: []pipeline.ProcessRef{{PID: 43, Name: name, Exe: `C:\fake\demo.exe`, CPUPercent: 2, RSS: 2097152}}}},
		} {
			if err := f.db.Write(ctx, message); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := f.db.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	seedAPIInventory(t, f)
}

func TestHTTPDiagnosticReadScopeOpenAPIPrivacyAndMissingID(t *testing.T) {
	f := newAPIFixture(t)
	seedAPIDiagnostic(t, f)
	response := f.request(t, "GET", "/api/v1/alerts/fake-diagnostic/diagnostics", "read", "", 200)
	var value store.DiagnosticPackage
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil || value.SchemaVersion != 1 || len(value.Snapshots) != 3 || len(value.Events) != 1 || value.Events[0].RecordID != "9007199254740993" {
		t.Fatalf("diagnostic HTTP shape: %v %s", err, response.Body)
	}
	for _, secret := range []string{"fake-private", "fake-other-host", "instanceKey", "password"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("private or unscoped data exported: %s", secret)
		}
	}
	f.request(t, "GET", "/api/v1/alerts/fake-diagnostic/diagnostics", "invalid", "", 401)
	f.request(t, "GET", "/api/v1/alerts/missing/diagnostics", "read", "", 404)
	f.request(t, "GET", "/api/v1/alerts/"+strings.Repeat("x", 257)+"/diagnostics", "read", "", 400)
	response = f.request(t, "GET", "/api/v1/openapi.json", "read", "", 200)
	if !strings.Contains(response.Body.String(), "/api/v1/alerts/{id}/diagnostics") || !strings.Contains(response.Body.String(), "rawCompleteFrom") {
		t.Fatal("diagnostic endpoint or typed coverage missing from OpenAPI")
	}
	if f.files.writes != 0 || len(f.client.sent) != 0 || len(f.toast.values) != 0 {
		t.Fatal("read-only diagnostics caused side effects")
	}
}
