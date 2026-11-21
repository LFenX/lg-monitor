// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
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
	_ "modernc.org/sqlite"
)

func TestValidateMigrationTargetRejectsUnsafePaths(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	nonEmpty := filepath.Join(t.TempDir(), "non-empty")
	if err := os.MkdirAll(nonEmpty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "keep.txt"), []byte("x"), 0o600); err != nil {
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
		{"non_empty", nonEmpty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validateMigrationTarget(source, tc.target, program); err == nil {
				t.Fatalf("expected error for target %q", tc.target)
			}
		})
	}
}

func TestStorageMigrationCopiesSQLiteAndConfigAndWritesRedirect(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "old-home")
	target := filepath.Join(t.TempDir(), "new-home")
	if err := os.MkdirAll(filepath.Join(source, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}

	// Write config, log, and a real live WAL-mode SQLite database.
	if err := os.WriteFile(filepath.Join(source, "config", "settings.yaml"), []byte("httpListen: 127.0.0.1:7444\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "logs", "lgmon.log"), []byte("{\"msg\":\"ok\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(source, "data", "lgmon.db")
	db, err := store.Open(ctx, store.Options{Path: dbPath, Now: time.Now})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	// Seed an extra table/row in WAL mode to verify VACUUM INTO preserves live WAL transactions.
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS migrate_check (k TEXT PRIMARY KEY, v TEXT)"); err != nil {
		rawDB.Close()
		t.Fatal(err)
	}
	if _, err := rawDB.ExecContext(ctx, "INSERT INTO migrate_check (k, v) VALUES ('hello', 'world')"); err != nil {
		rawDB.Close()
		t.Fatal(err)
	}
	rawDB.Close()

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

	// Start migration through storageOperation.
	body, _ := json.Marshal(api.StorageMigrateInput{Target: target})
	res, err := backend.storageOperation(ctx, api.MigrateStorage, api.Request{Body: body})
	if err != nil {
		t.Fatalf("start migration: %v", err)
	}
	initial := res.(api.StorageStatus)
	if initial.Migration == nil {
		t.Fatal("expected initial migration status")
	}

	// Wait for background migration to complete.
	backend.background.Wait()

	final := backend.storageStatus()
	if final.Migration == nil || final.Migration.State != "done" || final.Migration.Percent != 100 || !final.Migration.NeedsRestart {
		t.Fatalf("unexpected final migration state: %+v", final.Migration)
	}

	// Verify copied config & SQLite row & redirect file.
	gotCfg, err := os.ReadFile(filepath.Join(target, "config", "settings.yaml"))
	if err != nil || string(gotCfg) != "httpListen: 127.0.0.1:7444\n" {
		t.Fatalf("copied settings.yaml mismatch: %q %v", string(gotCfg), err)
	}
	dstDB, err := sql.Open("sqlite", filepath.Join(target, "data", "lgmon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dstDB.Close()
	var val string
	if err := dstDB.QueryRowContext(ctx, "SELECT v FROM migrate_check WHERE k = 'hello'").Scan(&val); err != nil || val != "world" {
		t.Fatalf("migrated sqlite row = %q, err = %v", val, err)
	}
	if resolved, redirected := config.ResolveHome(source); !redirected || !config.SamePath(resolved, target) {
		t.Fatalf("redirect not written: resolved=%q redirected=%v", resolved, redirected)
	}
}
