package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func diagnosticFixture(t *testing.T) (*Store, *View, int64) {
	t.Helper()
	now := int64(2000000000000)
	anchor := now - 3600000
	s := openStore(t, Options{DisableMaintenance: true, Now: func() time.Time { return time.UnixMilli(now) }})
	ctx := context.Background()
	if err := s.RegisterCollector(ctx, CollectorRecord{ID: "fake-diag", Hostname: "虚构诊断主机", FirstSeen: 1, LastSeen: now}); err != nil {
		t.Fatal(err)
	}
	alert := rules.Alert{ID: "fake-diagnostic", RuleID: "fake-rule", CollectorID: "fake-diag", TargetID: "web", State: rules.StateFiring, Severity: "critical", StartedAt: anchor, SourceTS: anchor, Summary: "fake-private-summary", InstanceKey: "fake-private-instance"}
	if err := s.SaveTransition(ctx, alert, rules.Event{AlertID: alert.ID, TS: anchor, Kind: alert.State, Message: "fake-private-transition"}); err != nil {
		t.Fatal(err)
	}
	put := func(message pipeline.Message) {
		if err := s.Write(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	for _, host := range []string{"fake-diag", "other-host"} {
		for i, at := range []int64{anchor - 1, now - 1000} {
			name := "fake.exe"
			if host == "other-host" {
				name = "other-host.exe"
			}
			put(pipeline.Message{CollectorID: host, Seq: uint64(i + 1), TS: at, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: "up", Process: collector.ProcessFound, Reason: "fake-private-reason", Processes: []pipeline.ProcessRef{{PID: int32(42 + i), Name: name, Exe: `C:\fake\fake.exe`, CreatedAt: 1, CPUPercent: 5, RSS: 1024, Threads: 3}}}})
		}
		put(pipeline.Message{CollectorID: host, Seq: 3, TS: anchor, Kind: pipeline.KindProbeResult, ProbeResult: &collector.ProbeResult{TargetID: "web", Type: "http", StatusCode: 503, Error: "https://fake:fake-private-url@fake.invalid/?token=fake-private-token", Message: "fake-private-probe", LatencyMS: 25}})
		put(pipeline.Message{CollectorID: host, Seq: 4, TS: anchor, Kind: pipeline.KindSystemMetrics, SystemMetrics: &collector.SystemMetrics{TS: anchor, CPUPercent: 5}})
		event := eventLogMessage(5, anchor)
		event.CollectorID = host
		event.EventLog.RecordID = 9007199254740993
		event.EventLog.Message = "fake-private-event"
		event.EventLog.Targets = []collector.EventLogTarget{{ID: "web", Via: "both"}}
		if host == "other-host" {
			event.EventLog.Subject = "other-host-event.exe"
		}
		put(event)
	}
	other := eventLogMessage(6, anchor)
	other.CollectorID = "fake-diag"
	other.EventLog.RecordID = 7
	other.EventLog.Subject = "other-target.exe"
	other.EventLog.Targets = []collector.EventLogTarget{{ID: "other", Via: "subject"}}
	put(other)
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO inventory_changes(collector_id,kind,scope,seq,ts,id,name,change,old_fingerprint,new_fingerprint) VALUES('fake-diag','startup','machine/64/Run',50,?,'fake','虚构启动变化','changed','fake-private-old','fake-private-new')`, anchor); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO collector_events(collector_id,seq,ts,kind,message,dropped,source_table,gap_from,gap_to) VALUES('fake-diag',99,?,'data_gap','fake-private-gap',3,'process_samples',1,3)`, anchor); err != nil {
		t.Fatal(err)
	}
	v, err := OpenViewWithClock(ctx, s.maintenanceView.path, func() time.Time { return time.UnixMilli(now) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return s, v, anchor
}

func TestDiagnosticPackageDownKeepsPreviousMatchAndBoundsProcesses(t *testing.T) {
	s, v, anchor := diagnosticFixture(t)
	status := pipeline.TargetStatus{TargetID: "web", State: "up", Process: collector.ProcessFound}
	for i := range 60 {
		status.Processes = append(status.Processes, pipeline.ProcessRef{PID: int32(i + 100), Name: "虚构进程.exe", Exe: strings.Repeat("虚", 1000)})
	}
	detail, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE target_status SET detail=? WHERE collector_id='fake-diag' AND ts=?`, string(detail), anchor-1); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(context.Background(), pipeline.Message{CollectorID: "fake-diag", Seq: 100, TS: anchor, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: "down", Process: collector.ProcessMissing}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	value, err := v.DiagnosticPackage(context.Background(), "fake-diagnostic")
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Snapshots) != 3 || value.Snapshots[0].State != "down" || len(value.Snapshots[0].Processes) != 0 {
		t.Fatal("down observation was replaced by an earlier running observation")
	}
	previous := value.Snapshots[2]
	if previous.Role != "previousMatch" || previous.TS != anchor-1 || len(previous.Processes) != 50 || !previous.ProcessesTruncated || previous.Processes[0].PID != 100 {
		t.Fatal("missing or unbounded previous-match evidence")
	}
	if len(previous.Processes[0].Exe) > 2048 || !utf8.ValidString(previous.Processes[0].Exe) || value.Coverage[0].Rows != 3 || value.Coverage[0].Limit != 3 {
		t.Fatal("process fields or coverage exceed declared bounds")
	}
	if _, err := s.db.Exec(`UPDATE alerts SET rule_id=? WHERE id='fake-diagnostic'`, strings.Repeat("x", DiagnosticMaxBytes)); err != nil {
		t.Fatal(err)
	}
	if _, err := v.DiagnosticPackage(context.Background(), "fake-diagnostic"); !errors.Is(err, ErrDiagnosticSize) {
		t.Fatal("oversized package was accepted", err)
	}
}

func TestDiagnosticPackagePendingWindowUsesCollectorClockWithoutRewritingHistory(t *testing.T) {
	s, v, anchor := diagnosticFixture(t)
	for _, item := range []struct {
		offset  int64
		pending bool
	}{{3600000, true}, {-3600000, false}} {
		if _, err := s.db.Exec(`UPDATE collectors SET clock_offset_ms=? WHERE id='fake-diag'`, item.offset); err != nil {
			t.Fatal(err)
		}
		value, err := v.DiagnosticPackage(context.Background(), "fake-diagnostic")
		if err != nil || value.Window.Pending != item.pending || value.Window.Anchor != anchor || value.Events[0].TS != anchor {
			t.Fatal("clock offset changed history or misidentified pending data", err)
		}
	}
}

func TestDiagnosticPackageScopesMetadataRedactsRawFieldsAndKeepsIncidentSeparate(t *testing.T) {
	_, v, anchor := diagnosticFixture(t)
	value, err := v.DiagnosticPackage(context.Background(), "fake-diagnostic")
	if err != nil {
		t.Fatal(err)
	}
	if value.SchemaVersion != 1 || value.Window.Anchor != anchor || value.Window.Pending || len(value.Coverage) != 6 || len(value.Snapshots) != 2 {
		t.Fatalf("diagnostic shape=%+v", value)
	}
	if value.Snapshots[0].Role != "incident" || value.Snapshots[0].Processes[0].PID != 42 || value.Snapshots[1].Role != "latest" || value.Snapshots[1].Processes[0].PID != 43 {
		t.Fatal("incident and latest process facts mixed")
	}
	if len(value.Probes) != 1 || value.Probes[0].Type != "http" || value.Probes[0].OK || value.Probes[0].LatencyMS != 25 || len(value.Metrics) != 1 || len(value.Events) != 1 || value.Events[0].RecordID != "9007199254740993" || len(value.Changes) != 1 || len(value.CollectorEvents) != 1 {
		t.Fatalf("evidence missing: %+v", value)
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) > DiagnosticMaxBytes {
		t.Fatal("encoding budget", err)
	}
	for _, private := range []string{"fake-private", "other-host.exe", "other-host-event.exe", "other-target.exe", "https://fake", "instanceKey", "notificationKey", "error\"", "message\""} {
		if strings.Contains(string(data), private) {
			t.Fatalf("unscoped or private field exported: %s", private)
		}
	}
	if value.Host == nil || value.Host.Name != "虚构诊断主机" || !strings.Contains(value.AgentPrompt, "不执行") {
		t.Fatal("handoff lacks host or trust boundary")
	}
}

func TestDiagnosticPackageRetainedTruncatedOldTableGlobalAndCancellation(t *testing.T) {
	s, v, anchor := diagnosticFixture(t)
	for i := int64(10); i < 70; i++ {
		if _, err := s.db.Exec(`INSERT INTO eventlog_events(collector_id,channel,record_id,ts,provider,event_id,level,subject,message) VALUES('fake-diag','Application',?,?,'Application Error',1000,2,'fake.exe','fake-private-event')`, i, anchor); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO eventlog_matches(collector_id,channel,record_id,ts,kind,name) VALUES('fake-diag','Application',?,?,'subject','web')`, i, anchor); err != nil {
			t.Fatal(err)
		}
	}
	value, err := v.DiagnosticPackage(context.Background(), "fake-diagnostic")
	if err != nil {
		t.Fatal(err)
	}
	var truncated bool
	for _, coverage := range value.Coverage {
		if coverage.Section == "eventLogs" {
			truncated = coverage.Truncated
		}
	}
	if !truncated || len(value.Events) != 50 {
		t.Fatal("event tail truncation hidden")
	}
	if err := s.cleanRawTable(context.Background(), "eventlog_events", anchor+1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE inventory_changes`); err != nil {
		t.Fatal(err)
	}
	value, err = v.DiagnosticPackage(context.Background(), "fake-diagnostic")
	if err != nil {
		t.Fatal(err)
	}
	var expired, old bool
	for _, coverage := range value.Coverage {
		if coverage.Section == "eventLogs" {
			expired = !coverage.Available && coverage.RawCompleteFrom == anchor+1 && coverage.Reason != ""
		}
		if coverage.Section == "startupChanges" {
			old = !coverage.Available && strings.Contains(coverage.Reason, "版本")
		}
	}
	if !expired || !old {
		t.Fatal("retention or missing schema misreported as complete")
	}
	if _, err := s.db.Exec(`UPDATE alerts SET collector_id='',source_ts=? WHERE id='fake-diagnostic'`, value.GeneratedAt); err != nil {
		t.Fatal(err)
	}
	value, err = v.DiagnosticPackage(context.Background(), "fake-diagnostic")
	if err != nil {
		t.Fatal(err)
	}
	if value.Host != nil || len(value.Snapshots) != 0 || len(value.Events) != 0 || !value.Window.Pending {
		t.Fatal("global alert expanded collection scope or hid pending window")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := v.DiagnosticPackage(ctx, "fake-diagnostic"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	if _, err := v.DiagnosticPackage(context.Background(), "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("missing alert not identified", err)
	}
	if _, err := v.DiagnosticPackage(context.Background(), strings.Repeat("x", 257)); !errors.Is(err, ErrDiagnosticID) {
		t.Fatal("oversized ID accepted", err)
	}
}
