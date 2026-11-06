//go:build integration && windows

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestNativeTokenStoreCanBeManagedWhileMonitorDatabaseIsOpen(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(home, "data", "lgmon.db"), DisableMaintenance: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	deps := defaultTokenDependencies()
	if err := requireTokenAdmin(deps); err != nil {
		t.Fatal(err)
	}
	backend, err := deps.Open(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	hash := api.TokenHash("fake-native-cli-value")
	created, err := backend.AddAPIToken(ctx, hash[:], "fake-native-cli", "read")
	if err != nil {
		backend.Close()
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if err := secrets.NativePathPolicy().Check(filepath.Join(home, "data", "auth.db")); err != nil {
		t.Fatal(err)
	}
	live, err := store.OpenAuthDB(ctx, filepath.Join(home, "data", "auth.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	backend, err = deps.Open(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	rows, err := backend.ListAPITokens(ctx)
	if err != nil || len(rows) != 1 || len(rows[0].Hash) != 0 {
		t.Fatalf("native metadata: %+v %v", rows, err)
	}
	if err := backend.RevokeAPIToken(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := live.Authenticate(ctx, hash[:]); err != nil || found {
		t.Fatalf("native revocation did not apply: %v", err)
	}
}
