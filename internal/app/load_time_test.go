// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
	_ "modernc.org/sqlite"
)

// writerOnly fails every read: the collector list must come from the read-only connection.
type writerOnly struct{ *store.Store }

func (writerOnly) Collectors(context.Context) ([]store.CollectorRecord, error) {
	panic("the collector list was read on the writer's connection")
}

func TestPresenceListsCollectorsFromTheReadOnlyConnection(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1000000)
	path := filepath.Join(t.TempDir(), "monitor.db")
	db, err := store.Open(ctx, store.Options{Path: path, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.RegisterCollector(ctx, store.CollectorRecord{ID: "fake-host", Hostname: "虚构主机", FirstSeen: 900000, LastSeen: 999000}); err != nil {
		t.Fatal(err)
	}
	view, err := store.OpenViewWithClock(ctx, path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	view.AllowReaders(ctx, 4)
	link := transport.NewReceiver(nil)
	defer link.Close()
	source := pairedPresence{pairs: &listedPairs{items: []transport.Credential{{ID: "fake-host", IssuedAt: 900000}}}, db: writerOnly{db}, read: view, link: link, now: func() time.Time { return now }, startedAt: now.UnixMilli()}
	items, err := source.Collectors(ctx)
	if err != nil || len(items) != 1 || items[0].Hostname != "虚构主机" || items[0].LastSeen != 999000 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

// countingSQL answers every history query with no rows and counts them.
type countingSQL struct{ queries int }

func (c *countingSQL) Query(context.Context, string) (rules.QueryResult, error) {
	c.queries++
	return rules.QueryResult{}, nil
}
func (c *countingSQL) QueryParams(context.Context, string, map[string]any) (rules.QueryResult, error) {
	c.queries++
	return rules.QueryResult{}, nil
}
func (c *countingSQL) Schema(context.Context) (store.DatabaseSchema, error) {
	return store.DatabaseSchema{}, nil
}
func (c *countingSQL) VisitMonitorSamples(context.Context, string, []string, int64, int64, func(string, string, int64, string)) error {
	c.queries++
	return nil
}

func TestHistoryIsSharedForAFewSecondsEvenWhileSomethingIsDown(t *testing.T) {
	now := time.UnixMilli(2_000_000_000_000)
	sql := &countingSQL{}
	b := &APIBackend{options: APIBackendOptions{SQL: sql, Now: func() time.Time { return now }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	manager, err := config.NewManager(config.FSLoader{FS: fstest.MapFS{
		"targets.yaml": {Data: []byte("targets: [{id: site, match: {port: 3000}}]")},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	scan := func(failing bool) int {
		before := sql.queries
		if _, err := b.history(context.Background(), manager.Snapshot(), failing); err != nil {
			t.Fatal(err)
		}
		return sql.queries - before
	}
	if scan(true) == 0 {
		t.Fatal("the first request did not scan")
	}
	// Every open page refreshes on each sample; a failure used to make each of those refreshes rescan 24 hours.
	now = now.Add(5 * time.Second)
	if n := scan(true); n != 0 {
		t.Fatalf("rescanned %d times within the failing window", n)
	}
	now = now.Add(failingHistoryTTL)
	if scan(true) == 0 {
		t.Fatal("a failure's bars were never refreshed")
	}
	// All healthy again: the longer window applies once the cached result is from a healthy scan.
	now = now.Add(failingHistoryTTL)
	scan(false)
	now = now.Add(20 * time.Second)
	if n := scan(false); n != 0 {
		t.Fatalf("healthy history rescanned after 20s: %d", n)
	}
	now = now.Add(historyTTL)
	if scan(false) == 0 {
		t.Fatal("healthy history never refreshed")
	}
	// A new configuration is a new answer.
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if scan(false) == 0 {
		t.Fatal("a changed configuration reused old history")
	}
}

// sqlFallbackOnly hides targetSampleVisitor so tests can compare the streaming path against SQL fallback.
type sqlFallbackOnly struct{ SQLSource }

func TestMonitorDetailStreamMatchesSQLFallback(t *testing.T) {
	f := newAPIFixture(t)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"主站","id":"site","port":3000}`, 200)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"隧道","id":"tunnel","process":"fake-tunnel.exe","parent":"site"}`, 200)
	seq := uint64(0)
	write := func(collectorID, target, state, reason string, offset time.Duration) {
		seq++
		if err := f.db.Write(context.Background(), pipeline.Message{
			CollectorID: collectorID, Seq: seq, TS: f.now.Add(offset).UnixMilli(),
			Kind:         pipeline.KindTargetStatus,
			TargetStatus: &pipeline.TargetStatus{TargetID: target, State: state, Reason: reason},
		}); err != nil {
			t.Fatal(err)
		}
	}
	for offset := -90 * time.Minute; offset <= 0; offset += 10 * time.Second {
		if offset > -45*time.Minute && offset < -40*time.Minute {
			continue // sampling gap
		}
		state, reason := "up", "运行正常"
		if (offset >= -70*time.Minute && offset < -66*time.Minute) || (offset >= -25*time.Minute && offset < -24*time.Minute-40*time.Second) {
			state, reason = "down", "端口未监听"
		}
		write("fake-host", "site", state, reason, offset)
		write("fake-host", "tunnel", map[bool]string{true: "down", false: "up"}[offset >= -3*time.Minute && offset < -1*time.Minute], "隧道断开", offset)
	}
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := f.manager.Snapshot()
	f.backend.agent.history, f.backend.agent.listed = nil, nil
	streamed, err := f.backend.monitorDetail(context.Background(), cfg, "site")
	if err != nil {
		t.Fatal(err)
	}
	fallbackOpts := f.backend.options
	fallbackOpts.SQL = sqlFallbackOnly{f.backend.options.SQL}
	fallbackBackend, err := NewAPIBackend(fallbackOpts)
	if err != nil {
		t.Fatal(err)
	}
	sqlResult, err := fallbackBackend.monitorDetail(context.Background(), cfg, "site")
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(streamed)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(sqlResult)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("streamed detail diverged from SQL fallback:\ngot:  %s\nwant: %s", got, want)
	}
}

func BenchmarkMonitorListAndDetail(b *testing.B) {
	ctx := context.Background()
	now := time.UnixMilli(2_000_000_000_000)
	path := filepath.Join(b.TempDir(), "bench.db")
	db, err := store.Open(ctx, store.Options{Path: path, Now: func() time.Time { return now }, DisableMaintenance: true})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	if err := db.RegisterCollector(ctx, store.CollectorRecord{ID: "bench-host", Hostname: "BENCH-PC", FirstSeen: now.Add(-24 * time.Hour).UnixMilli(), LastSeen: now.UnixMilli(), Version: "dev"}); err != nil {
		b.Fatal(err)
	}
	// Seed 16 targets x 10,800 samples (172,800 target_status rows + 5,400 probe_results rows) directly in one tx.
	var targetsYAML string
	targetsYAML = "targets:\n"
	for i := 0; i < 16; i++ {
		id := fmt.Sprintf("svc-%02d", i)
		targetsYAML += fmt.Sprintf("  - id: %s\n    name: 服务 %02d\n    match:\n      port: %d\n", id, i, 18000+i)
		if i == 1 || i == 2 {
			targetsYAML += "    parent: svc-00\n"
		}
		if i == 0 || i == 3 {
			targetsYAML += fmt.Sprintf("    probes:\n      - type: http\n        url: http://127.0.0.1:%d/\n        interval: 15s\n        timeout: 2s\n", 18000+i)
		}
	}
	manager, err := config.NewManager(config.FSLoader{FS: fstest.MapFS{
		"targets.yaml": {Data: []byte(targetsYAML)},
	}}, nil)
	if err != nil {
		b.Fatal(err)
	}
	if err := db.Close(); err != nil {
		b.Fatal(err)
	}
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: url.Values{"_pragma": {"journal_mode(WAL)", "synchronous(OFF)"}}.Encode()}
	rawDB, err := sql.Open("sqlite", uri.String())
	if err != nil {
		b.Fatal(err)
	}
	tx, err := rawDB.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	stmtStatus, err := tx.PrepareContext(ctx, `INSERT INTO target_status(collector_id,target_id,ts,state,detail) VALUES('bench-host',?,?,?,?)`)
	if err != nil {
		b.Fatal(err)
	}
	stmtProbe, err := tx.PrepareContext(ctx, `INSERT INTO probe_results(collector_id,target_id,probe,ts,ok,latency_ms,error,type,message,tls_not_after,tls_subject) VALUES('bench-host',?,'0',?,?,?,?, 'http','',NULL,'')`)
	if err != nil {
		b.Fatal(err)
	}
	upDetail := `{"targetId":"svc","state":"up","process":"found","port":"listening","reason":"端口正在监听","processes":[{"pid":1234,"ppid":1,"createdAt":1999900000000,"name":"demo.exe","exe":"C:\\demo\\demo.exe","cpuPercent":0.4,"rss":42000000,"threads":12}]}`
	downDetail := `{"targetId":"svc","state":"down","process":"missing","port":"closed","reason":"进程未运行"}`
	start := now.Add(-6 * time.Hour).UnixMilli()
	for i := 0; i < 16; i++ {
		id := fmt.Sprintf("svc-%02d", i)
		for step := 0; step < 10800; step++ {
			ts := start + int64(step)*2000
			state, detail := "up", upDetail
			if (step >= 3000 && step < 3090) || (step >= 7000 && step < 7010) {
				state, detail = "down", downDetail
			}
			if _, err := stmtStatus.ExecContext(ctx, id, ts, state, detail); err != nil {
				b.Fatal(err)
			}
			if (i == 0 || i == 3) && step%8 == 0 {
				okVal, errText := 1, ""
				if state == "down" {
					okVal, errText = 0, "connection refused"
				}
				if _, err := stmtProbe.ExecContext(ctx, id, ts, okVal, 12+(step%7), errText); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
	stmtStatus.Close()
	stmtProbe.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	rawDB.Close()

	db, err = store.Open(ctx, store.Options{Path: path, Now: func() time.Time { return now }, DisableMaintenance: true})
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	view, err := store.OpenViewWithClock(ctx, path, func() time.Time { return now })
	if err != nil {
		b.Fatal(err)
	}
	defer view.Close()
	view.AllowReaders(ctx, 4)
	link := transport.NewReceiver(nil)
	defer link.Close()
	source := pairedPresence{pairs: &listedPairs{items: []transport.Credential{{ID: "bench-host", IssuedAt: start}}}, db: db, read: view, link: link, now: func() time.Time { return now }, startedAt: start}
	backend := &APIBackend{options: APIBackendOptions{Config: manager, Read: view, SQL: view, Collectors: source, Now: func() time.Time { return now }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	cfg := manager.Snapshot()

	b.Run("ListMonitorsCold", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			backend.agent.history, backend.agent.listed = nil, nil
			items, err := backend.monitors(ctx, cfg)
			if err != nil || len(items) != 16 {
				b.Fatalf("len=%d err=%v", len(items), err)
			}
		}
	})
	b.Run("MonitorDetailParentWithProbes", func(b *testing.B) {
		// Prime monitors cache like a real click from board -> detail page.
		if _, err := backend.monitors(ctx, cfg); err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := backend.monitorDetail(ctx, cfg, "svc-00"); err != nil {
				b.Fatal(err)
			}
		}
	})
}
