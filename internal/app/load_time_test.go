package app

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
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

func TestHistoryIsSharedForAFewSecondsEvenWhileSomethingIsDown(t *testing.T) {
	now := time.UnixMilli(2_000_000_000_000)
	sql := &countingSQL{}
	b := &APIBackend{options: APIBackendOptions{SQL: sql, Now: func() time.Time { return now }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	port := 3000
	cfg := &config.Config{Targets: []config.Target{{ID: "site", Match: config.Match{Port: &port}}}}
	scan := func(failing bool) int {
		before := sql.queries
		if _, err := b.history(context.Background(), cfg, failing); err != nil {
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
	cfg = &config.Config{Targets: cfg.Targets}
	if scan(false) == 0 {
		t.Fatal("a changed configuration reused old history")
	}
}
