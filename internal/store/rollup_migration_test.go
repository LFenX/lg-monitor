package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUpgradeQueuesExistingRawDataBeforeRetention(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	now := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	start := now.Add(-48 * time.Hour).UnixMilli()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY,applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		version, err := strconv.Atoi(strings.SplitN(filepath.Base(file), "_", 2)[0])
		if err != nil {
			t.Fatal(err)
		}
		if version < 7 {
			if err := applyMigration(ctx, db, file, version, func() time.Time { return now }); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Exec(`INSERT INTO system_metrics(collector_id,ts,cpu,memory_total,memory_used,memory_available,memory_percent,uptime,detail) VALUES('fake-upgrade',?,75,1000,400,600,40,100,'{}')`, start); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s := openStore(t, Options{Path: path, Now: func() time.Time { return now }})
	if count(t, s, "rollup_dirty") != 1 || count(t, s, "system_metrics") != 1 {
		t.Fatal("upgrade lost source data or did not queue historical aggregation")
	}
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if samples, avg, max := rollupValue(t, s, "metric_hour", "system.cpu_percent", start); samples != 1 || avg != 75 || max != 75 || count(t, s, "system_metrics") != 0 {
		t.Fatal("upgrade pruned raw history before creating correct rollups")
	}
}

func TestAggregationWaitsForBucketEndAndPreservesRawBoundaryBucket(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	now := start.Add(30 * time.Second)
	s := openStore(t, Options{Now: func() time.Time { return now }})
	writeMetric(t, s, start.Add(10*time.Second).UnixMilli(), 10)
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "metric_minute") != 0 || count(t, s, "metric_hour") != 0 {
		t.Fatal("open time bucket was aggregated")
	}
	writeMetric(t, s, start.Add(40*time.Second).UnixMilli(), 90)
	now = start.Add(24*time.Hour + 30*time.Second)
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "system_metrics") != 2 {
		t.Fatal("partial boundary bucket lost source samples")
	}
	if samples, avg, max := rollupValue(t, s, "metric_minute", "system.cpu_percent", start.UnixMilli()); samples != 2 || avg != 50 || max != 90 {
		t.Fatal("boundary bucket aggregated incomplete raw data")
	}
	now = start.Add(25 * time.Hour)
	if err := s.Prune(ctx); err != nil || count(t, s, "system_metrics") != 0 {
		t.Fatalf("completed expired source bucket was not pruned: %v", err)
	}
}
