package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestCollectorCacheHeartbeatPersistenceAndLegacyClearing(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{DisableMaintenance: true})
	if err := s.RegisterCollector(ctx, CollectorRecord{ID: "fake-cache", Hostname: "FakeCache", FirstSeen: 1, LastSeen: 1}); err != nil {
		t.Fatal(err)
	}
	rate := 50000000.0
	usage, err := pipeline.NewCacheUsage(1000000, 500000000, &rate)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat(ctx, "fake-cache", &usage); err != nil {
		t.Fatal(err)
	}
	view, err := OpenView(ctx, s.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	for _, read := range []func() (CollectorRecord, bool, error){
		func() (CollectorRecord, bool, error) { return s.Collector(ctx, "fake-cache") },
		func() (CollectorRecord, bool, error) { return view.Collector(ctx, "fake-cache") },
	} {
		row, found, err := read()
		if err != nil || !found || row.Cache == nil || row.Cache.Bytes != usage.Bytes || row.Cache.MaxBytes != usage.MaxBytes ||
			row.Cache.BytesPerHour == nil || *row.Cache.BytesPerHour != rate || *row.Cache.ProjectedHours != *usage.ProjectedHours {
			t.Fatalf("persisted cache: %+v %v", row, err)
		}
	}
	list, err := view.Collectors(ctx)
	if err != nil || len(list) != 1 || list[0].Cache == nil {
		t.Fatalf("status list cache: %+v %v", list, err)
	}
	if err := s.Heartbeat(ctx, "fake-cache"); err != nil {
		t.Fatal(err)
	}
	row, _, err := view.Collector(ctx, "fake-cache")
	if err != nil || row.Cache != nil {
		t.Fatalf("legacy heartbeat left stale statistics: %+v %v", row, err)
	}
}

func TestReadOnlyCollectorCacheIsOptionalBeforeMigration14(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, "CREATE TABLE collectors(id TEXT PRIMARY KEY,hostname TEXT,first_seen INTEGER,last_seen INTEGER,version TEXT,clock_offset_ms INTEGER NOT NULL DEFAULT 0); INSERT INTO collectors VALUES('fake-old','FakeOld',1,2,'fake-v13',0)")
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	view, err := OpenView(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	row, found, err := view.Collector(ctx, "fake-old")
	if err != nil || !found || row.Cache != nil || row.Version != "fake-v13" {
		t.Fatalf("legacy reader: %+v %v", row, err)
	}
	list, err := view.Collectors(ctx)
	if err != nil || len(list) != 1 || list[0].Cache != nil {
		t.Fatalf("legacy status list: %+v %v", list, err)
	}
}
