package config

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

type memoryLoader struct {
	mu    sync.Mutex
	files fstest.MapFS
}

func (l *memoryLoader) Load() (*Config, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Load(l.files)
}

func (l *memoryLoader) replace(t *testing.T, file, from, to string) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	replaceFile(t, l.files, file, from, to)
}

func TestReloadRetainsLastValidConfiguration(t *testing.T) {
	loader := &memoryLoader{files: validFiles()}
	var logs bytes.Buffer
	manager, err := NewManager(loader, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	loader.replace(t, "targets.yaml", "demo.exe", "changed.exe")
	loader.replace(t, "rules.yaml", "target: web", "target: absent")
	if err := manager.Reload(); err == nil {
		t.Fatal("invalid configuration was accepted")
	}
	if got := manager.Snapshot().Targets[0].Match.Process; got != "demo.exe" {
		t.Fatalf("partially applied an invalid multi-file configuration: %q", got)
	}
	if !strings.Contains(logs.String(), "Configuration reload failed") {
		t.Fatal("reload failure was not logged")
	}
	loader.replace(t, "rules.yaml", "target: absent", "target: web")
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := manager.Snapshot().Targets[0].Match.Process; got != "changed.exe" {
		t.Fatalf("fixed configuration was not applied: %q", got)
	}
}

func TestSnapshotIsIsolated(t *testing.T) {
	files := validFiles()
	replaceFile(t, files, "targets.yaml", "process: demo.exe", "process: demo.exe\n      port: 18080")
	files["card-templates/raw-demo.yaml"] = &fstest.MapFile{Data: []byte("id: raw-demo\nraw: {schema: '2.0', body: {elements: [{tag: markdown, content: original}]}}\n")}
	manager, err := NewManager(FSLoader{FS: files}, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := manager.Snapshot()
	snapshot.Targets[0].Match.Process = "mutated.exe"
	*snapshot.Targets[0].Match.Port = 1
	snapshot.Rules[0].Notify[0] = "mutated"
	body := snapshot.Templates[1].Raw.(map[string]any)["body"].(map[string]any)
	body["elements"].([]any)[0].(map[string]any)["content"] = "mutated"
	current := manager.Snapshot()
	if current.Targets[0].Match.Process != "demo.exe" || *current.Targets[0].Match.Port != 18080 || current.Rules[0].Notify[0] != "alerts" {
		t.Fatal("snapshot mutation changed the active configuration")
	}
	currentBody := current.Templates[1].Raw.(map[string]any)["body"].(map[string]any)
	if currentBody["elements"].([]any)[0].(map[string]any)["content"] != "original" {
		t.Fatal("raw template mutation changed the active configuration")
	}
}

type fakeChanges struct {
	changes chan struct{}
	errors  chan error
	closed  atomic.Bool
}

func (s *fakeChanges) Changes() <-chan struct{} { return s.changes }
func (s *fakeChanges) Errors() <-chan error     { return s.errors }
func (s *fakeChanges) Close() error             { s.closed.Store(true); return nil }

type recordingHandler struct{ messages chan string }

func (h recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h recordingHandler) Handle(_ context.Context, record slog.Record) error {
	h.messages <- record.Message
	return nil
}
func (h recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h recordingHandler) WithGroup(string) slog.Handler      { return h }

func waitMessage(t *testing.T, messages <-chan string, want string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case got := <-messages:
		if !strings.Contains(got, want) {
			t.Fatalf("log message = %q, want %q", got, want)
		}
	case <-timer.C:
		t.Fatal("timed out waiting for the configuration event")
	}
}

func TestWatchRejectsInvalidThenRecovers(t *testing.T) {
	loader := &memoryLoader{files: validFiles()}
	messages := make(chan string, 10)
	manager, err := NewManager(loader, slog.New(recordingHandler{messages: messages}))
	if err != nil {
		t.Fatal(err)
	}
	source := &fakeChanges{changes: make(chan struct{}), errors: make(chan error)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Watch(ctx, source) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("watcher shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("watcher did not shut down")
		}
		if !source.closed.Load() {
			t.Error("change source was not closed")
		}
	})
	source.errors <- errors.New("fake watcher error")
	waitMessage(t, messages, "Configuration watcher error")
	loader.replace(t, "rules.yaml", "target: web", "target: missing")
	source.changes <- struct{}{}
	waitMessage(t, messages, "Configuration reload failed")
	if manager.Snapshot().Rules[0].Target != "web" {
		t.Fatal("invalid configuration replaced the active snapshot")
	}
	loader.replace(t, "rules.yaml", "target: missing", "target: web")
	loader.replace(t, "targets.yaml", "demo.exe", "recovered.exe")
	source.changes <- struct{}{}
	waitMessage(t, messages, "Configuration reloaded")
	if manager.Snapshot().Targets[0].Match.Process != "recovered.exe" {
		t.Fatal("watcher failed to apply the corrected configuration")
	}
}

func TestManagerRejectsInvalidInitialConfiguration(t *testing.T) {
	files := validFiles()
	replaceFile(t, files, "rules.yaml", "for: 10s", "for: invalid")
	if manager, err := NewManager(FSLoader{FS: files}, nil); err == nil || manager != nil {
		t.Fatalf("NewManager returned %v, %v", manager, err)
	}
}

func TestSubscriptionsPublishOnlyLatestValidIsolatedSnapshot(t *testing.T) {
	loader := &memoryLoader{files: validFiles()}
	manager, err := NewManager(loader, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, second := manager.Subscribe(ctx), manager.Subscribe(ctx)
	initial := <-first
	initial.Targets[0].Match.Process = "mutated.exe"
	if (<-second).Targets[0].Match.Process != "demo.exe" || manager.Snapshot().Targets[0].Match.Process != "demo.exe" {
		t.Fatal("subscribers share mutable configuration")
	}
	loader.replace(t, "targets.yaml", "demo.exe", "next.exe")
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	loader.replace(t, "targets.yaml", "next.exe", "latest.exe")
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	for _, subscription := range []<-chan *Config{first, second} {
		if (<-subscription).Targets[0].Match.Process != "latest.exe" {
			t.Fatal("subscription retained an obsolete revision")
		}
	}
	loader.replace(t, "rules.yaml", "target: web", "target: missing")
	if err := manager.Reload(); err == nil {
		t.Fatal("bad reload succeeded")
	}
	select {
	case <-first:
		t.Fatal("invalid configuration was published")
	default:
	}
	cancel()
	select {
	case _, ok := <-first:
		if ok {
			t.Fatal("cancelled subscription remained open")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("subscription cancellation timed out")
	}
	if _, ok := <-second; ok {
		t.Fatal("second subscription did not close")
	}
}
