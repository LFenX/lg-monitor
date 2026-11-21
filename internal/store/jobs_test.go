// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package store

import (
	"context"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestJobEventsAreKeptOnceAndARunStaysOnOneBusinessDay(t *testing.T) {
	s := openStore(t, Options{DisableMaintenance: true})
	ctx := context.Background()
	row := func(id, attempt, date, fallback, body string) JobEventRow {
		return JobEventRow{JobID: "aics", EventID: id, AttemptID: attempt, BusinessDate: date, Fallback: fallback, TS: 1794940000000, Source: "api", Body: []byte(body)}
	}
	result, err := s.AppendJobEvents(ctx, []JobEventRow{
		row("e1", "a1", "2026-11-17", "", `{"n":1}`),
		// The run crosses midnight without saying its date: it stays on the day it started.
		row("e2", "a1", "", "2026-11-18", `{"n":2}`),
		row("e3", "a2", "", "2026-11-18", `{"n":3}`),
	})
	if err != nil || result != (JobAppendResult{Accepted: 3}) {
		t.Fatalf("first report: %+v %v", result, err)
	}
	// Resending is harmless; a different body under a used ID is refused and counted.
	result, err = s.AppendJobEvents(ctx, []JobEventRow{row("e1", "a1", "2026-11-17", "", `{"n":1}`), row("e2", "a1", "", "2026-11-18", `{"n":9}`)})
	if err != nil || result != (JobAppendResult{Duplicates: 1, Conflicts: 1}) {
		t.Fatalf("resend: %+v %v", result, err)
	}
	events, err := s.maintenanceView.JobEvents(ctx, "aics", "2026-11-17", "2026-11-17")
	if err != nil || len(events) != 2 || events[1].EventID != "e2" || string(events[1].Body) != `{"n":2}` {
		t.Fatalf("day one: %+v %v", events, err)
	}
	if events, _ := s.maintenanceView.JobEvents(ctx, "aics", "2026-11-18", "2026-11-18"); len(events) != 1 || events[0].AttemptID != "a2" {
		t.Fatalf("day two: %+v", events)
	}
}

func TestJobFileLinesAreStoredOnceAndTheFileStateFollowsTheCollector(t *testing.T) {
	s := openStore(t, Options{DisableMaintenance: true})
	ctx := context.Background()
	seq := uint64(0)
	write := func(record collector.JobEventRecord) {
		t.Helper()
		seq++
		if err := s.Write(ctx, pipeline.Message{CollectorID: "fake-collector", Seq: seq, TS: 1794938700000, Kind: pipeline.KindJobEvent, JobEvent: &record}); err != nil {
			t.Fatal(err)
		}
		if err := s.Flush(ctx); err != nil {
			t.Fatal(err)
		}
	}
	source := func() JobSource {
		t.Helper()
		value, found, err := s.maintenanceView.JobSource(ctx, "aics-upload")
		if err != nil || !found {
			t.Fatalf("source = %+v %t %v", value, found, err)
		}
		return value
	}
	const path = `D:\aics\events.jsonl`
	event := []byte(`{"event_id":"e1","attempt_id":"a1","kind":"stage","stage":"fetch","state":"success","ts":1794938700000}`)
	write(collector.JobEventRecord{JobID: "aics-upload", Path: path, Error: "读取任务事件文件失败：文件不存在"})
	if got := source(); got.State != "error" || got.ProblemLine != 0 || got.CollectorID != "fake-collector" || got.LastEventAt != 0 {
		t.Fatalf("missing file = %+v", got)
	}
	write(collector.JobEventRecord{JobID: "aics-upload", Path: path})
	write(collector.JobEventRecord{JobID: "aics-upload", Path: path, Line: 1, Event: event})
	// The file was rotated and the line read again: still one event.
	write(collector.JobEventRecord{JobID: "aics-upload", Path: path, Line: 1, Event: event})
	write(collector.JobEventRecord{JobID: "aics-upload", Path: path, Line: 2, Error: "第 2 行：不是合法事件"})
	// A collector that skipped its own check still cannot store an event with forbidden fields.
	write(collector.JobEventRecord{JobID: "aics-upload", Path: path, Line: 3, Event: []byte(`{"event_id":"e2","mail_body":"hello"}`)})
	got := source()
	if got.State != "ok" || got.ProblemLine != 3 || !strings.Contains(got.Problem, "第 3 行") || got.LastEventAt == 0 {
		t.Fatalf("after recovery and line problems = %+v", got)
	}
	stored, err := s.maintenanceView.JobEvents(ctx, "aics-upload", "2026-11-18", "2026-11-18")
	if err != nil || len(stored) != 1 || stored[0].Source != "collector:fake-collector" || stored[0].EventID != "e1" {
		t.Fatalf("stored = %+v %v", stored, err)
	}
}
