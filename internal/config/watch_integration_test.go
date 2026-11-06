//go:build integration

package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	write("card-templates/extra.yml", "id: extra\nextends: standard\n")
	if err := manager.Reload(); err == nil || !strings.Contains(err.Error(), "模板文件请使用 .yaml 扩展名") || len(manager.Snapshot().Templates) != 0 {
		t.Fatalf("yml creation was silently ignored: %v", err)
	}
	write("card-templates/extra.yml", "id: extra\nextends: compact\n")
	if err := manager.Reload(); err == nil || !strings.Contains(err.Error(), "extra.yml") {
		t.Fatalf("yml modification was silently ignored: %v", err)
	}
	if err := os.Rename(filepath.Join(directory, "card-templates", "extra.yml"), filepath.Join(directory, "card-templates", "extra.yaml")); err != nil {
		t.Fatal(err)
	}
	waitChange()
	if err := manager.Reload(); err != nil || len(manager.Snapshot().Templates) != 1 || manager.Snapshot().Templates[0].Extends != "compact" {
		t.Fatalf("renaming yml to yaml did not recover: %v", err)
	}
	write("settings.yaml", "retention: {raw: 48h, minute: 720h, hour: 17520h}\n")
	if err := manager.Reload(); err != nil || manager.Snapshot().Settings.Retention.Raw != "48h" {
		t.Fatalf("settings file did not reload: %v", err)
	}
	write("settings.yaml", "retention: {raw: 0s}\n")
	if err := manager.Reload(); err == nil || manager.Snapshot().Settings.Retention.Raw != "48h" {
		t.Fatalf("invalid settings replaced active retention: %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
}
