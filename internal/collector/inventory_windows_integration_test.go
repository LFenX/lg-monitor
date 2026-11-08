//go:build windows && integration

package collector

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeInventoryOnlyReadsExistingTasksRegistryAndDirectoryMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	source, err := NewInventorySource([]byte(strings.Repeat("f", 32)))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := source.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tasks, success := 0, 0
	for _, snapshot := range rows {
		if err := snapshot.Validate(); err != nil {
			t.Fatal(err)
		}
		if snapshot.ErrorCode == "" {
			success++
		}
		if snapshot.Kind == "tasks" {
			if snapshot.ErrorCode != "" {
				t.Fatalf("native Task Scheduler read failed: %s", snapshot.ErrorCode)
			}
			tasks = len(snapshot.Entries)
		}
	}
	if success < 2 || tasks == 0 {
		t.Fatalf("native coverage: successful=%d tasks=%d", success, tasks)
	}
	directory := t.TempDir()
	file := filepath.Join(directory, "fake-startup.txt")
	if err := os.WriteFile(file, []byte("fake-private-file-content"), 0600); err != nil {
		t.Fatal(err)
	}
	native := &nativeInventory{key: []byte(strings.Repeat("f", 32))}
	entries, err := native.readStartupDirectory(ctx, directory)
	if err != nil || len(entries) != 1 || entries[0].Name != "fake-startup.txt" {
		t.Fatal("metadata enumeration failed", err)
	}
	first := entries[0].Fingerprint
	updated := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(file, updated, updated); err != nil {
		t.Fatal(err)
	}
	entries, err = native.readStartupDirectory(ctx, directory)
	if err != nil || entries[0].Fingerprint == first {
		t.Fatal("metadata update not detected", err)
	}
	if _, err := native.readStartupDirectory(ctx, `\\fake.invalid\share\Startup`); err == nil {
		t.Fatal("UNC directory accepted")
	}
	link := filepath.Join(t.TempDir(), "redirect")
	if err := os.Symlink(directory, link); err == nil {
		if _, err := native.readStartupDirectory(ctx, link); err == nil {
			t.Fatal("startup directory symlink followed")
		}
	}
	t.Logf("read-only native coverage: %d scopes, %d tasks; no task/registry/startup registrations", success, tasks)
}
