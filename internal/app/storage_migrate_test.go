// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"bytes"
	"context"
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

	// A previous partial migration directory with only lgmon subdirectories must be reusable on retry.
	if got, err := validateMigrationTarget(source, partialRetry, program); err != nil || got != partialRetry {
		t.Fatalf("partial retry directory should be accepted: got=%q err=%v", got, err)
	}
}

func TestStorageMigrationSkipsLockedOutboxAndCompactsLogs(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "old-home")
	target := filepath.Join(t.TempDir(), "new-home")
	for _, sub := range []string{"config", "data", "logs"} {
		if err := os.MkdirAll(filepath.Join(source, sub), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "collector-id"), []byte("col-test-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "config", "settings.yaml"), []byte("httpListen: 127.0.0.1:7444\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Write a 3 MiB log file to verify both in-place compaction and migration log tailing trim it <= 2 MiB.
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

	// Seed an active Outbox holding a real Windows mandatory byte-range lock on data/outbox.db.lock!
	outbox, err := transport.OpenOutbox(ctx, transport.OutboxOptions{
		Path:        filepath.Join(source, "data", "outbox.db"),
		CollectorID: "col-test-1",
		Now:         time.Now,
	})
	if err != nil {
		t.Fatalf("OpenOutbox: %v", err)
	}
	defer outbox.Close()

	// Pre-populate target with a partial file from an earlier failed migration attempt to test retry cleanup.
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

	// Test CompactStorage first: 3.3 MiB log file should be trimmed to <= 2 MiB.
	compactAny, err := backend.storageOperation(ctx, api.CompactStorage, api.Request{})
	if err != nil {
		t.Fatalf("CompactStorage: %v", err)
	}
	compactRes := compactAny.(api.StorageCompactResult)
	if compactRes.FreedBytes <= 0 {
		t.Fatalf("expected CompactStorage to free log bytes, got %+v", compactRes)
	}

	// Now run full migration while outbox.db.lock is actively locked by OpenOutbox!
	body, _ := json.Marshal(api.StorageMigrateInput{Target: target})
	if _, err := backend.storageOperation(ctx, api.MigrateStorage, api.Request{Body: body}); err != nil {
		t.Fatalf("start migration: %v", err)
	}
	backend.background.Wait()

	final := backend.storageStatus()
	if final.Migration == nil || final.Migration.State != "done" || final.Migration.Percent != 100 || !final.Migration.NeedsRestart {
		t.Fatalf("unexpected final migration state: %+v", final.Migration)
	}

	// Verify synthesized outbox.db in target opens cleanly and matches collector ID.
	migratedOutbox, err := transport.OpenOutbox(ctx, transport.OutboxOptions{
		Path:        filepath.Join(target, "data", "outbox.db"),
		CollectorID: "col-test-1",
		Now:         time.Now,
	})
	if err != nil {
		t.Fatalf("migrated outbox failed to open: %v", err)
	}
	_ = migratedOutbox.Close()
}
