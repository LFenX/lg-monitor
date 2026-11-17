package store

import (
	"context"
	"testing"
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
