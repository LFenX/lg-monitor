package transport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
)

type memoryTargetCache struct {
	mu      sync.Mutex
	version string
	data    []byte
	saves   int
	failure error
}

func (m *memoryTargetCache) Load(context.Context) (string, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		return "", nil, os.ErrNotExist
	}
	return m.version, append([]byte(nil), m.data...), nil
}

func (m *memoryTargetCache) Save(_ context.Context, version string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failure != nil {
		return m.failure
	}
	m.version, m.data = version, append([]byte(nil), data...)
	m.saves++
	return nil
}

func testTargetConfig(id string) *config.Config {
	return &config.Config{Targets: []config.Target{{ID: id, Match: config.Match{Process: "fake.exe"}}}}
}

func TestCachedTargetsPublishOnlyAfterDurableValidSaveAndRestoreOffline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	storage := &memoryTargetCache{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cache, err := NewCachedTargets(ctx, storage, logger)
	if err != nil {
		t.Fatal(err)
	}
	updates := cache.Subscribe(ctx)
	if len((<-updates).Targets) != 0 {
		t.Fatal("missing cache was not empty")
	}
	version, data, _ := config.TargetConfig(testTargetConfig("first"), "fake-collector", "")
	if err := cache.Apply(ctx, version, data); err != nil {
		t.Fatal(err)
	}
	if snapshot := <-updates; len(snapshot.Targets) != 1 || snapshot.Targets[0].ID != "first" {
		t.Fatalf("wrong initial push: %+v", snapshot)
	}
	if err := cache.Apply(ctx, version, data); err != nil || storage.saves != 1 {
		t.Fatalf("duplicate version rewrote the cache: %v %d", err, storage.saves)
	}
	if err := cache.Apply(ctx, version, []byte(`{"targets":[]}`)); err == nil {
		t.Fatal("mismatched content accepted")
	}
	otherVersion, otherData, _ := config.TargetConfig(testTargetConfig("next"), "fake-collector", "")
	storage.failure = errors.New("fake disk failure")
	if err := cache.Apply(ctx, otherVersion, otherData); !errors.Is(err, storage.failure) {
		t.Fatalf("save error hidden: %v", err)
	}
	if cache.Snapshot().Targets[0].ID != "first" || storage.version != version {
		t.Fatal("failed update replaced the good configuration")
	}
	select {
	case <-updates:
		t.Fatal("failed or duplicate update was published")
	default:
	}
	restarted, err := NewCachedTargets(ctx, storage, logger)
	if err != nil || restarted.Snapshot().Targets[0].ID != "first" {
		t.Fatalf("offline restore: %v", err)
	}
	storage.failure = nil
	emptyVersion, emptyData, _ := config.TargetConfig(&config.Config{}, "fake-collector", "")
	if err := cache.Apply(ctx, emptyVersion, emptyData); err != nil || len((<-updates).Targets) != 0 {
		t.Fatalf("target removal failed: %v", err)
	}
	storage.data = []byte("broken cache")
	if _, err := NewCachedTargets(ctx, storage, logger); err == nil {
		t.Fatal("silently discarded corrupt configuration cache")
	}
}

type configTicker struct{ ticks chan time.Time }

func (t *configTicker) Ticks() <-chan time.Time { return t.ticks }
func (*configTicker) Stop()                     {}

type configClock struct{ created chan *configTicker }

func (c *configClock) NewTicker(interval time.Duration) collector.ProbeTicker {
	if interval != 3*time.Second {
		panic("unexpected config retry interval")
	}
	ticker := &configTicker{ticks: make(chan time.Time, 1)}
	c.created <- ticker
	return ticker
}

type observedApply struct {
	cache    *CachedTargets
	versions chan string
	reject   atomic.Bool
}

func (a *observedApply) Apply(ctx context.Context, version string, data []byte) error {
	a.versions <- version
	if a.reject.Swap(false) {
		return errors.New("fake save failure")
	}
	return a.cache.Apply(ctx, version, data)
}

func TestConfigurationPushWhileDataAckIsPendingRetriesAndReconnects(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager, err := config.NewManager(initialTargets{testTargetConfig("initial")}, logger)
	if err != nil {
		t.Fatal(err)
	}
	clock := &configClock{created: make(chan *configTicker, 4)}
	receiver.SetConfigDelivery(ConfigDelivery{Source: manager, Clock: clock, Logger: logger})
	cache, err := NewCachedTargets(ctx, &memoryTargetCache{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	applier := &observedApply{cache: cache, versions: make(chan string, 8)}
	applier.reject.Store(true)
	writer, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector", Hostname: "fake-host"}, applier)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	var ticker *configTicker
	select {
	case ticker = <-clock.created:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	readVersion := func(want string) {
		t.Helper()
		select {
		case got := <-applier.versions:
			if got != want {
				t.Fatalf("version %s want %s", got, want)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	initial, _, _ := config.TargetConfig(manager.Snapshot(), "fake-collector", "fake-host")
	readVersion(initial)
	if len(cache.Snapshot().Targets) != 0 {
		t.Fatal("failed initial push became active")
	}
	ticker.ticks <- time.Time{}
	readVersion(initial)
	updates := cache.Subscribe(ctx)
	for snapshot := range updates {
		if len(snapshot.Targets) == 1 {
			break
		}
	}
	writeResult := make(chan error, 1)
	go func() { writeResult <- writer.Write(ctx, wireMessage(1)) }()
	message, err := receiver.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(testTargetConfig("changed")); err != nil {
		t.Fatal(err)
	}
	changed, _, _ := config.TargetConfig(manager.Snapshot(), "fake-collector", "fake-host")
	readVersion(changed)
	receiver.Acknowledge(message, nil)
	select {
	case err := <-writeResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("configuration acknowledgement blocked data acknowledgement")
	}
	writer.Close()
	var reconnected *Writer
	for ctx.Err() == nil {
		reconnected, err = NewWriter(ctx, connection, Hello{CollectorID: "fake-collector", Hostname: "fake-host", LastAckedSeq: 1}, applier)
		if err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if reconnected == nil {
		t.Fatal(err)
	}
	defer reconnected.Close()
	readVersion(changed)
	if err := manager.Apply(&config.Config{}); err != nil {
		t.Fatal(err)
	}
	empty, _, _ := config.TargetConfig(&config.Config{}, "fake-collector", "fake-host")
	readVersion(empty)
}
