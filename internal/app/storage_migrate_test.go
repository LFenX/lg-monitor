// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
	_ "modernc.org/sqlite"
)

func TestValidateMigrationTargetRejectsUnsafePathsAndAllowsPartialRetry(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	foreignNonEmpty := filepath.Join(t.TempDir(), "foreign-non-empty")
	if err := os.MkdirAll(foreignNonEmpty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreignNonEmpty, "my-photo.jpg"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	partialRetry := filepath.Join(t.TempDir(), "partial-retry")
	if err := os.MkdirAll(filepath.Join(partialRetry, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(partialRetry, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(t.TempDir(), "Program", "lgmon.exe")

	cases := []struct {
		name   string
		target string
	}{
		{"empty", ""},
		{"relative", "relative/dir"},
		{"same", source},
		{"child", filepath.Join(source, "nested")},
		{"parent", filepath.Dir(source)},
		{"program_dir", filepath.Dir(program)},
		{"foreign_non_empty", foreignNonEmpty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validateMigrationTarget(source, tc.target, program); err == nil {
				t.Fatalf("expected error for target %q", tc.target)
			}
		})
	}

	if got, err := validateMigrationTarget(source, partialRetry, program); err != nil || got != partialRetry {
		t.Fatalf("partial retry directory should be accepted: got=%q err=%v", got, err)
	}
}

func TestStorageMigrationAlignsOutboxWithRealDataCollectorIDAndCompactsSafely(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "old-home")
	target := filepath.Join(t.TempDir(), "new-home")
	for _, sub := range []string{"config", "data", "logs"} {
		if err := os.MkdirAll(filepath.Join(source, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	const collectorID = "11111111-2222-4333-8444-555555555555"
	// Store collector-id at the real collector.FileIDStore path: <home>/data/collector-id.
	if err := os.WriteFile(filepath.Join(source, "data", "collector-id"), []byte(collectorID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "config", "settings.yaml"), []byte("httpListen: 127.0.0.1:7444\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hugeLog := bytes.Repeat([]byte("{\"msg\":\"line\"}\n"), 220_000)
	if err := os.WriteFile(filepath.Join(source, "logs", "lgmon.log"), hugeLog, 0o600); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(source, "data", "lgmon.db")
	db, err := store.Open(ctx, store.Options{Path: dbPath, Now: time.Now})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	// Seed a non-zero committed sequence in lgmon.db to verify outbox watermark alignment!
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.ExecContext(ctx, `INSERT INTO collector_offsets(collector_id, stored_seq, applied_seq) VALUES(?, 500, 500)`, collectorID); err != nil {
		rawDB.Close()
		t.Fatal(err)
	}
	rawDB.Close()

	// Seed an active Outbox holding a real Windows mandatory byte-range lock on data/outbox.db.lock!
	outbox, err := transport.OpenOutbox(ctx, transport.OutboxOptions{
		Path:        filepath.Join(source, "data", "outbox.db"),
		CollectorID: collectorID,
		Now:         time.Now,
	})
	if err != nil {
		t.Fatalf("OpenOutbox: %v", err)
	}
	defer outbox.Close()

	// Pre-populate target with a partial file from an earlier failed migration attempt.
	if err := os.MkdirAll(filepath.Join(target, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "data", "stale.txt"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	manager, err := config.NewManager(config.FSLoader{FS: fstest.MapFS{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	docs := &apiDocuments{MapFS: fstest.MapFS{}}
	editor, err := config.NewEditor(docs, manager)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.OpenViewWithClock(ctx, dbPath, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	backend, err := NewAPIBackend(APIBackendOptions{
		Config: manager,
		Editor: editor,
		Read:   view,
		SQL:    view,
		Write:  db,
		Collectors: presenceSourceFunc(func(context.Context) ([]rules.CollectorPresence, error) {
			return nil, nil
		}),
		Secrets:   &apiVault{values: map[string][]byte{}},
		NewClient: func(config.FeishuApp, []byte) (feishu.Client, error) { return nil, nil },
		Now:       time.Now,
		Home:      source,
	})
	if err != nil {
		t.Fatal(err)
	}
	backend.SetOutboxCompactor(outbox.CompactNow)

	// Test CompactStorage: trims 3.3 MiB log and compacts outbox safely under Outbox.mu without breaking outbox.
	compactAny, err := backend.storageOperation(ctx, api.CompactStorage, api.Request{})
	if err != nil {
		t.Fatalf("CompactStorage: %v", err)
	}
	if compactAny.(api.StorageCompactResult).FreedBytes <= 0 {
		t.Fatalf("expected CompactStorage to free log bytes")
	}
	if _, err := outbox.State(ctx); err != nil {
		t.Fatalf("outbox became unusable after CompactStorage: %v", err)
	}

	// Run full migration while outbox.db.lock is actively locked!
	body, _ := json.Marshal(api.StorageMigrateInput{Target: target})
	if _, err := backend.storageOperation(ctx, api.MigrateStorage, api.Request{Body: body}); err != nil {
		t.Fatalf("start migration: %v", err)
	}
	backend.background.Wait()

	final := backend.storageStatus()
	if final.Migration == nil || final.Migration.State != "done" || final.Migration.Percent != 100 || !final.Migration.NeedsRestart {
		t.Fatalf("unexpected final migration state: %+v", final.Migration)
	}

	// Verify synthesized outbox.db in target aligns with applied_seq=500 and passes SyncResume(501)!
	migratedOutbox, err := transport.OpenOutbox(ctx, transport.OutboxOptions{
		Path:        filepath.Join(target, "data", "outbox.db"),
		CollectorID: collectorID,
		Now:         time.Now,
	})
	if err != nil {
		t.Fatalf("migrated outbox failed to open: %v", err)
	}
	defer migratedOutbox.Close()
	st, err := migratedOutbox.State(ctx)
	if err != nil || st.Allocated != 500 || st.Acked != 500 {
		t.Fatalf("migrated outbox watermark mismatch: %+v err=%v", st, err)
	}
	if err := migratedOutbox.SyncResume(ctx, 501); err != nil {
		t.Fatalf("migrated outbox SyncResume(501) failed: %v", err)
	}
}

func TestAlignLocalOutboxHealsDivergedMigrationState(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	const collectorID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"

	// Simulate an existing migrated directory where OpenOutbox created a fresh outbox (allocated=0, acked=0)
	// while lgmon.db already has applied_seq=900.
	box, err := transport.OpenOutbox(ctx, transport.OutboxOptions{
		Path:        filepath.Join(home, "data", "outbox.db"),
		CollectorID: collectorID,
		Now:         time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = box.Close()

	healed, err := alignLocalOutboxWithMonitor(ctx, home, collectorID, 900)
	if err != nil || !healed {
		t.Fatalf("alignLocalOutboxWithMonitor = (%v, %v), want (true, nil)", healed, err)
	}

	reopened, err := transport.OpenOutbox(ctx, transport.OutboxOptions{
		Path:        filepath.Join(home, "data", "outbox.db"),
		CollectorID: collectorID,
		Now:         time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.SyncResume(ctx, 901); err != nil {
		t.Fatalf("healed outbox SyncResume(901) failed: %v", err)
	}
}
