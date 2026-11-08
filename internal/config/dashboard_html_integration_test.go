//go:build integration

package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeHTMLDashboardFileEditsTriggerReload(t *testing.T) {
	directory := t.TempDir()
	pages := filepath.Join(directory, "dashboards")
	if err := os.Mkdir(pages, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pages, "custom.yaml"), []byte(htmlDashboardFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(pages, "custom.html")
	if err := os.WriteFile(name, []byte("<h1>原生初版</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(FSLoader{FS: os.DirFS(directory)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	watcher, err := NewFileWatcher(directory)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	updates := manager.Subscribe(ctx)
	first := <-updates
	done := make(chan error, 1)
	go func() { done <- manager.Watch(ctx, watcher) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("configuration watcher did not stop")
		}
	}()
	if err := os.WriteFile(name, []byte("<h1>原生第二版</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case current := <-updates:
			if len(current.Dashboards) == 1 && strings.Contains(current.Dashboards[0].HTMLContent, "原生第二版") && current.DashboardStates[0].Version != first.DashboardStates[0].Version {
				return
			}
		case <-timer.C:
			t.Fatal("HTML-only edit was not observed and reloaded")
		}
	}
}
