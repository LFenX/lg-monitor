package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
)

type listedPairs struct {
	transport.PairRepository
	items []transport.Credential
	err   error
}

type presenceSourceFunc func(context.Context) ([]rules.CollectorPresence, error)

func (f presenceSourceFunc) Collectors(ctx context.Context) ([]rules.CollectorPresence, error) {
	return f(ctx)
}

func TestAutomaticOfflineNotificationRemainsPendingUntilChannelConfigured(t *testing.T) {
	f := newRuntimeFixture(t)
	files := fstest.MapFS{}
	manager, err := config.NewManager(config.FSLoader{FS: files}, f.opts.Logger)
	if err != nil {
		t.Fatal(err)
	}
	f.opts.Config = manager
	f.opts.Source = transport.NewReceiver(nil)
	lastSeen := f.clock.Now().Add(-30 * time.Second).UnixMilli()
	f.opts.Collectors = presenceSourceFunc(func(context.Context) ([]rules.CollectorPresence, error) {
		return []rules.CollectorPresence{{ID: f.opts.ID, Hostname: "虚构主机", LastSeen: lastSeen}}, nil
	})
	if err := f.db.RegisterCollector(context.Background(), store.CollectorRecord{ID: f.opts.ID, Hostname: "虚构主机", FirstSeen: lastSeen, LastSeen: lastSeen}); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	ticker := receiveRuntime(t, f.clock.created)
	f.clock.tick(ticker, time.Second)
	for {
		alerts, err := f.db.ActiveAlerts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(alerts) == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("offline incident was not persisted without channels")
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case <-f.client.wire:
		t.Fatal("sent before any channel was configured")
	default:
	}
	files["notifiers.yaml"] = f.files["notifiers.yaml"]
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	for {
		f.clock.tick(ticker, time.Second)
		select {
		case card := <-f.client.wire:
			if card.State != rules.StateFiring {
				t.Fatalf("wrong deferred offline notification: %+v", card)
			}
			if attempt := receiveRuntime(t, f.db.attempts); !attempt.OK {
				t.Fatal("offline delivery not audited")
			}
			return
		case <-ctx.Done():
			t.Fatal("channel addition did not retry pending offline notification")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (s *listedPairs) List(context.Context) ([]transport.Credential, error) { return s.items, s.err }

func TestPairedPresenceIncludesNeverConnectedAndExcludesRevoked(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1000000)
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "monitor.db"), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	link := transport.NewReceiver(nil)
	defer link.Close()
	pairs := &listedPairs{items: []transport.Credential{{ID: "never-connected", Hostname: "fake-host", IssuedAt: 900000}, {ID: "revoked", Revoked: true, IssuedAt: 900000}}}
	source := pairedPresence{pairs: pairs, db: db, link: link, now: func() time.Time { return now }, startedAt: now.UnixMilli()}
	items, err := source.Collectors(ctx)
	if err != nil || len(items) != 1 || items[0].ID != "never-connected" || items[0].LastSeen != 900000 || items[0].Connected {
		t.Fatalf("wrong paired population: %+v %v", items, err)
	}
	if err := db.Heartbeat(ctx, "never-connected"); err != nil {
		t.Fatal(err)
	}
	items, err = source.Collectors(ctx)
	if err != nil || items[0].LastSeen != now.UnixMilli() {
		t.Fatalf("paired baseline replaced newer contact: %+v %v", items, err)
	}
	if err := db.RegisterCollector(ctx, store.CollectorRecord{ID: "never-connected", LastSeen: now.Add(time.Hour).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	items, err = source.Collectors(ctx)
	if err != nil || items[0].LastSeen != source.startedAt {
		t.Fatalf("legacy future timestamp hid a missing collector: %+v %v", items, err)
	}
	pairs.err = errors.New("fake pairing read failure")
	if _, err := source.Collectors(ctx); !errors.Is(err, pairs.err) {
		t.Fatalf("registry failure hidden: %v", err)
	}
}
