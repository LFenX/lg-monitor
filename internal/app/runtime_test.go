package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/store"
)

type runtimeVault struct{}

func (runtimeVault) Get(context.Context, string) ([]byte, error) {
	return []byte("fake-runtime-secret"), nil
}

type runtimeClient struct {
	wire       chan feishu.CardMessage
	release    chan struct{}
	rejectOnce atomic.Bool
	active     atomic.Int32
	closed     atomic.Int32
}

func (c *runtimeClient) TenantToken(context.Context) (string, error)       { return "fake-token", nil }
func (c *runtimeClient) BotInfo(context.Context) (feishu.Bot, error)       { return feishu.Bot{}, nil }
func (c *runtimeClient) AppScopes(context.Context) ([]feishu.Scope, error) { return nil, nil }
func (c *runtimeClient) ListChats(context.Context) ([]feishu.Chat, error)  { return nil, nil }
func (c *runtimeClient) ChatInfo(context.Context, string) (feishu.Chat, error) {
	return feishu.Chat{}, nil
}
func (c *runtimeClient) CreateChat(context.Context, feishu.NewChat) (feishu.Chat, error) {
	panic("runtime must not create chats")
}
func (c *runtimeClient) Close() error { c.closed.Add(1); return nil }
func (c *runtimeClient) SendCard(ctx context.Context, message feishu.CardMessage) (feishu.SendResult, error) {
	c.active.Add(1)
	defer c.active.Add(-1)
	c.wire <- message
	if c.rejectOnce.Swap(false) {
		return feishu.SendResult{}, &feishu.Error{Kind: feishu.CardRejected, Code: 230099, Message: "虚构卡片被拒绝"}
	}
	if message.State == "resolved" {
		select {
		case <-c.release:
		case <-ctx.Done():
			return feishu.SendResult{}, ctx.Err()
		}
	}
	return feishu.SendResult{MessageID: "om_fake_" + message.State}, nil
}

type runtimeStore struct {
	*store.Store
	client     *runtimeClient
	attempts   chan notify.Attempt
	writes     chan pipeline.Message
	earlyClose atomic.Bool
	closed     atomic.Bool
	writeError error
}

func (s *runtimeStore) RecordNotification(ctx context.Context, attempt notify.Attempt) error {
	if err := s.Store.RecordNotification(ctx, attempt); err != nil {
		return err
	}
	s.attempts <- attempt
	return nil
}
func (s *runtimeStore) Write(ctx context.Context, message pipeline.Message) error {
	if s.writeError != nil {
		return s.writeError
	}
	err := s.Store.Write(ctx, message)
	if err == nil {
		s.writes <- message
	}
	return err
}
func (s *runtimeStore) Close() error {
	if s.client.active.Load() != 0 {
		s.earlyClose.Store(true)
	}
	s.closed.Store(true)
	return s.Store.Close()
}

type runtimeFixture struct {
	opts      RuntimeOptions
	source    *controlledProcesses
	clock     *fakeClock
	client    *runtimeClient
	db        *runtimeStore
	path      string
	files     fstest.MapFS
	factories atomic.Int32
}

func newRuntimeFixture(t *testing.T) *runtimeFixture {
	t.Helper()
	files := fstest.MapFS{
		"targets.yaml":               {Data: []byte("targets:\n- id: demo\n  match: {process: demo.exe}\n")},
		"rules.yaml":                 {Data: []byte("rules:\n- id: down\n  type: process_absent\n  target: demo\n  for: 0s\n  severity: critical\n  notify: [test-channel]\n  template: custom\n")},
		"notifiers.yaml":             {Data: []byte("feishuApps:\n- id: default\n  appId: cli_fake_runtime\n  secretRef: fake-runtime-ref\n  domain: feishu\nnotifiers:\n- id: test-channel\n  type: feishu_app\n  app: default\n  chatId: oc_fake_runtime\n")},
		"card-templates/custom.yaml": {Data: []byte("id: custom\nraw:\n  schema: '2.0'\n  body:\n    elements:\n    - tag: fake-invalid-component\n")},
	}
	manager, err := config.NewManager(config.FSLoader{FS: files}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{created: make(chan *fakeTicker, 20)}
	clock.millis.Store(1_000_000)
	path := filepath.Join(t.TempDir(), "data", "lgmon.db")
	db, err := store.Open(context.Background(), store.Options{Path: path, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	client := &runtimeClient{wire: make(chan feishu.CardMessage, 20), release: make(chan struct{})}
	source := &controlledProcesses{requests: make(chan snapshotRequest, 20)}
	observed := &runtimeStore{Store: db, client: client, attempts: make(chan notify.Attempt, 20), writes: make(chan pipeline.Message, 20)}
	f := &runtimeFixture{source: source, clock: clock, client: client, db: observed, path: path, files: files}
	f.opts = RuntimeOptions{ID: "fake-collector", Hostname: "虚构主机", Config: manager, Store: observed, Processes: source, Ports: emptyPorts{},
		Probes:  executorFunc(func(context.Context, string, int, config.Probe) collector.ProbeResult { panic("unexpected probe") }),
		Secrets: runtimeVault{}, Now: clock.Now, Clock: clock, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		NewClient: func(config.FeishuApp, []byte) (feishu.Client, error) { f.factories.Add(1); return client, nil },
	}
	return f
}

func receiveRuntime[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for runtime event")
		var zero T
		return zero
	}
}

func TestRuntimePersistsNotifiesFallsBackAndDrainsOnCancel(t *testing.T) {
	f := newRuntimeFixture(t)
	f.client.rejectOnce.Store(true)
	runtime, err := NewRuntime(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	var collectTick *fakeTicker
	for i := 0; i < 2; i++ {
		ticker := receiveRuntime(t, f.clock.created)
		if ticker.interval == 2*time.Second {
			collectTick = ticker
		}
	}
	if collectTick == nil {
		t.Fatal("missing collection ticker")
	}
	first := receiveRuntime(t, f.source.requests)
	first.reply <- snapshotReply{rows: []collector.Process{{PID: 10, Name: "demo.exe", CreatedAt: 1}}}
	f.clock.tick(collectTick, 2*time.Second)
	second := receiveRuntime(t, f.source.requests)
	second.reply <- snapshotReply{}
	custom := receiveRuntime(t, f.client.wire)
	standard := receiveRuntime(t, f.client.wire)
	if custom.AlertID != standard.AlertID || custom.State != "firing" || standard.State != "firing" || !strings.Contains(string(standard.Card), "自定义模板 custom 出错，已使用标准模板") {
		t.Fatal("definite card rejection did not fall back with the same alert identity")
	}
	a, b := receiveRuntime(t, f.db.attempts), receiveRuntime(t, f.db.attempts)
	if a.OK || !b.OK || a.Template != "custom" || b.Template != "standard" {
		t.Fatalf("fallback audit mismatch: %+v %+v", a, b)
	}
	f.clock.tick(collectTick, 2*time.Second)
	third := receiveRuntime(t, f.source.requests)
	third.reply <- snapshotReply{rows: []collector.Process{{PID: 20, Name: "demo.exe", CreatedAt: 2}}}
	resolved := receiveRuntime(t, f.client.wire)
	if resolved.State != "resolved" || resolved.AlertID != custom.AlertID {
		t.Fatal("recovery lost alert identity")
	}
	cancel()
	close(f.client.release)
	if err := receiveRuntime(t, done); err != nil {
		t.Fatal(err)
	}
	recoveryAttempt := receiveRuntime(t, f.db.attempts)
	if !recoveryAttempt.OK || f.db.earlyClose.Load() || !f.db.closed.Load() || f.client.closed.Load() != 1 || f.factories.Load() != 1 {
		t.Fatal("shutdown did not finish sending and audit before closing cached clients and database")
	}
	view, err := store.OpenView(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	alerts, err := view.Alerts(context.Background(), true)
	if err != nil || len(alerts) != 1 || alerts[0].State != "resolved" || alerts[0].LastNotifiedAt == 0 {
		t.Fatalf("persisted recovery: %+v %v", alerts, err)
	}
	targets, err := view.CurrentTargets(context.Background())
	if err != nil || len(targets) != 1 || targets[0].State != "up" || !strings.Contains(string(targets[0].Detail), `"pid":20`) {
		t.Fatalf("tail records were not drained: %+v %v", targets, err)
	}
}

func TestRuntimeRetriesSourcesAndStopsForClosedStorage(t *testing.T) {
	for _, storage := range []bool{false, true} {
		t.Run(map[bool]string{false: "source", true: "storage"}[storage], func(t *testing.T) {
			f := newRuntimeFixture(t)
			sentinel := errors.New("controlled failure")
			if storage {
				f.db.writeError = errors.Join(sentinel, io.ErrClosedPipe)
			}
			runtime, err := NewRuntime(f.opts)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- runtime.Run(ctx) }()
			request := receiveRuntime(t, f.source.requests)
			if storage {
				request.reply <- snapshotReply{rows: []collector.Process{{PID: 10, Name: "demo.exe", CreatedAt: 1}}}
			} else {
				request.reply <- snapshotReply{err: sentinel}
				var collection *fakeTicker
				for i := 0; i < 2; i++ {
					ticker := receiveRuntime(t, f.clock.created)
					if ticker.interval == 2*time.Second {
						collection = ticker
					}
				}
				f.clock.tick(collection, 2*time.Second)
				receiveRuntime(t, f.source.requests).reply <- snapshotReply{rows: []collector.Process{{PID: 10, Name: "demo.exe", CreatedAt: 1}}}
				if written := receiveRuntime(t, f.db.writes); written.Kind != pipeline.KindTargetStatus || written.TargetStatus.State != "up" {
					t.Fatalf("source did not recover: %+v", written)
				}
				cancel()
				if err := receiveRuntime(t, done); err != nil || !f.db.closed.Load() {
					t.Fatalf("recovered runtime did not drain: %v", err)
				}
				return
			}
			if err := receiveRuntime(t, done); !errors.Is(err, sentinel) || !f.db.closed.Load() || f.factories.Load() != 0 {
				t.Fatalf("failure propagation or cleanup: %v", err)
			}
		})
	}
}
