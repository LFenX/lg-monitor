//go:build integration

package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileWatcherReload(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "config")
	source, err := NewFileWatcher(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	})
	manager, err := NewManager(FSLoader{FS: os.DirFS(directory)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	waitChange := func() {
		t.Helper()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		select {
		case _, ok := <-source.Changes():
			if !ok {
				t.Fatal("file watcher closed unexpectedly")
			}
		case err := <-source.Errors():
			t.Fatalf("file watcher error: %v", err)
		case <-timer.C:
			t.Fatal("file watcher did not detect the change")
		}
	}
	write := func(file, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, file), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		waitChange()
	}
	write("targets.yaml", "targets: [{id: demo, match: {process: first.exe}}]\n")
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	write("targets.yaml", "targets: [{id: demo, match: {cmdline: '['}}]\n")
	if err := manager.Reload(); err == nil || manager.Snapshot().Targets[0].Match.Process != "first.exe" {
		t.Fatalf("bad file changed the active configuration: %v", err)
	}
	temporary := filepath.Join(directory, "targets.tmp")
	if err := os.WriteFile(temporary, []byte("targets: [{id: demo, match: {process: second.exe}}]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, filepath.Join(directory, "targets.yaml")); err != nil {
		t.Fatal(err)
	}
	waitChange()
	if err := manager.Reload(); err != nil || manager.Snapshot().Targets[0].Match.Process != "second.exe" {
		t.Fatalf("atomic replacement did not reload: %v", err)
	}
	write("card-templates/oncall.yaml", "id: oncall\nextends: standard\n")
	if err := manager.Reload(); err != nil || len(manager.Snapshot().Templates) != 1 {
		t.Fatalf("template creation did not reload: %v", err)
	}
	write("card-templates/oncall.yaml", "id: oncall\nextends: nonexistent\n")
	if err := manager.Reload(); err == nil || manager.Snapshot().Templates[0].Extends != "standard" {
		t.Fatalf("invalid template replaced the previous snapshot: %v", err)
	}
	if err := os.Remove(filepath.Join(directory, "card-templates", "oncall.yaml")); err != nil {
		t.Fatal(err)
	}
	waitChange()
	if err := manager.Reload(); err != nil || len(manager.Snapshot().Templates) != 0 {
		t.Fatalf("template removal did not reload: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
}
