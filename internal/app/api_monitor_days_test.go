package app

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
)

func TestMonitorDayHalfFromSummariesHalfFromRawIsDrawnWhole(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	hour := time.Hour.Milliseconds()
	ceilHour := func(at time.Time) int64 { return (at.UnixMilli() + hour - 1) / hour * hour }
	local := f.now.Local()
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	yesterday, older := today.AddDate(0, 0, -1), today.AddDate(0, 0, -2)
	noon := ceilHour(yesterday.Add(12 * time.Hour))

	// Raw samples before noon yesterday have expired: those hours, and the day before, only live in status_hour.
	db, err := sql.Open("sqlite", f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for ts := ceilHour(older); ts < noon; ts += hour {
		if _, err := tx.ExecContext(ctx, `INSERT INTO status_hour(collector_id,target_id,ts,up,total,worst,confirmation_ms) VALUES('fake-host','web',?,180,180,2,60000)`, ts); err != nil {
			t.Fatal(err)
		}
	}
	for ts := noon; ts < today.UnixMilli(); ts += 20000 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO target_status(collector_id,target_id,ts,state,detail) VALUES('fake-host','web',?,'up','{}')`, ts); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	detail := decodeAs[api.MonitorDetail](t, f.request(t, "GET", "/api/v1/monitors/web", "read", "", 200).Body.Bytes())
	fallbackOpts := f.backend.options
	fallbackOpts.SQL = sqlFallbackOnly{fallbackOpts.SQL}
	fallback, err := NewAPIBackend(fallbackOpts)
	if err != nil {
		t.Fatal(err)
	}
	value, err := fallback.monitorDetail(ctx, f.manager.Snapshot(), "web")
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []struct {
		name   string
		detail api.MonitorDetail
	}{{"streaming", detail}, {"SQL fallback", value.(api.MonitorDetail)}} {
		t.Run(result.name, func(t *testing.T) {
			assertMixedMonitorDay(t, result.detail, older, yesterday, today, noon)
		})
	}
}

func assertMixedMonitorDay(t *testing.T, detail api.MonitorDetail, older, yesterday, today time.Time, noon int64) {
	t.Helper()
	day, before := detail.Days[len(detail.Days)-2], detail.Days[len(detail.Days)-3]
	if day.From != yesterday.UnixMilli() || before.From != older.UnixMilli() {
		t.Fatalf("days are not where expected: yesterday=%d older=%d", day.From, before.From)
	}
	if day.State != "up" || day.Uptime == nil || *day.Uptime != 100 {
		t.Fatalf("yesterday=%+v", day)
	}
	var covered, summarized int64
	for _, segment := range day.Segments {
		if segment.State != "up" {
			t.Fatalf("an all-up day drew %+v", segment)
		}
		covered += segment.To - segment.From
		if segment.Summary {
			summarized += segment.To - segment.From
			if segment.Uptime == nil || *segment.Uptime != 100 || segment.To > noon {
				t.Fatalf("summary range=%+v", segment)
			}
		}
	}
	if dayEnd := today.UnixMilli(); covered != dayEnd-day.From || summarized != noon-day.From {
		t.Fatalf("yesterday drawn %d of %d ms (summary %d); segments=%+v", covered, dayEnd-day.From, summarized, day.Segments)
	}
	if first := day.Segments[0]; !first.Summary || first.From != day.From {
		t.Fatalf("first segment=%+v", first)
	}
	if len(before.Segments) != 0 || before.State != "up" || before.Uptime == nil || *before.Uptime != 100 {
		t.Fatalf("a summary-only day keeps its plain daily share: %+v", before)
	}
}

func TestSummaryHoursKeepFailuresInTheirOwnHour(t *testing.T) {
	hour := time.Hour.Milliseconds()
	day := 24 * hour
	midnight := day + hour/2
	days := []api.StatusSlot{{From: 0}, {From: midnight, Segments: []api.StatusSegment{{From: 30 * hour, To: 32 * hour, State: "up"}}}}
	hours := []hourSummary{
		{ts: day + 2*hour, worst: 0, up: 170, all: 180},
		{ts: day, worst: 2, up: 90, all: 90},
		{ts: day, worst: 2, up: 90, all: 90},
		{ts: day + hour, worst: 2, up: 180, all: 180},
		{ts: day + 3*hour, worst: 2, up: 180, all: 180},
		{ts: day + 4*hour, worst: 1, up: 0, all: 0},
		{ts: 5 * hour, worst: 2, up: 180, all: 180},
	}
	attachSummaryHours(days, hours, 48*hour)
	if len(days[0].Segments) != 0 {
		t.Fatalf("summary-only day gained segments: %+v", days[0].Segments)
	}
	type shape struct {
		from, to int64
		state    string
		summary  bool
		uptime   float64
	}
	var got []shape
	for _, segment := range days[1].Segments {
		item := shape{from: segment.From, to: segment.To, state: segment.State, summary: segment.Summary, uptime: -1}
		if segment.Uptime != nil {
			item.uptime = float64(int(*segment.Uptime*100)) / 100
		}
		got = append(got, item)
	}
	want := []shape{
		{midnight, day + 2*hour, "up", true, 100},
		{day + 2*hour, day + 3*hour, "down", true, 94.44},
		{day + 3*hour, day + 4*hour, "up", true, 100},
		{day + 4*hour, day + 5*hour, "unknown", true, -1},
		{30 * hour, 32 * hour, "up", false, -1},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}
