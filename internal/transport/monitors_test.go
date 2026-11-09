package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
)

func TestNamedMonitorIdentityAndConfigIsolation(t *testing.T) {
	home, keys := t.TempDir(), newMemoryKeys()
	f1, f2 := newPairingFixture(t), newPairingFixture(t)
	ctx := f1.ctx
	var documents []clientDocument
	for index, fixture := range []*pairingFixture{f1, f2} {
		name := []string{"", "company"}[index]
		state := FileState{Home: home, Monitor: name}
		if _, err := Join(ctx, JoinOptions{Address: "monitor.test:7443", CollectorID: "same-collector", Hostname: "FakeHost", Code: fixture.code(t, time.Minute), Fingerprint: fixture.authority.Fingerprint(), Keys: keys, State: state, Connector: fixture.connector, Now: fixture.now}); err != nil {
			t.Fatal(err)
		}
		identity, err := LoadClient(ctx, state, keys, fixture.now)
		if err != nil || identity.ID != "same-collector" {
			t.Fatalf("identity: %+v %v", identity, err)
		}
		data, err := state.Load(ctx, "collector")
		if err != nil {
			t.Fatal(err)
		}
		var document clientDocument
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
		cache, err := NewCachedTargets(ctx, FileTargetCache{Home: home, Monitor: name}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		cfg := testTargetConfig("same-target")
		cfg.Targets[0].Match.Process = fmt.Sprintf("fake-%d.exe", index)
		version, raw, err := config.TargetConfig(cfg, "same-collector", "FakeHost")
		if err != nil {
			t.Fatal(err)
		}
		if err := cache.Apply(ctx, version, raw); err != nil {
			t.Fatal(err)
		}
	}
	if documents[0].KeyRef == documents[1].KeyRef || bytes.Equal(documents[0].CA, documents[1].CA) || bytes.Equal(documents[0].Certificate, documents[1].Certificate) {
		t.Fatal("shared private identity or trust")
	}
	for index, name := range []string{"", "company"} {
		cache, err := NewCachedTargets(ctx, FileTargetCache{Home: home, Monitor: name}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		update := <-cache.Subscribe(ctx)
		if update.Targets[0].Match.Process != fmt.Sprintf("fake-%d.exe", index) {
			t.Fatal("same target ID crossed monitors")
		}
	}
	state := FileState{Home: home, Monitor: "company"}
	if _, err := state.Load(ctx, "authority"); err == nil {
		t.Fatal("named monitor could read authority")
	}
	if err := state.Save(ctx, "endpoint", []byte(`{}`), true); err == nil {
		t.Fatal("named monitor could overwrite endpoint")
	}
	previous, _ := state.Load(ctx, "collector")
	if _, err := Join(ctx, JoinOptions{Address: "monitor.test:7443", CollectorID: "same-collector", Hostname: "FakeHost", Code: f1.code(t, time.Minute), Fingerprint: f1.authority.Fingerprint(), Keys: keys, State: state, Connector: f1.connector, Now: f1.now}); err == nil {
		t.Fatal("named trust could change")
	}
	after, _ := state.Load(ctx, "collector")
	if !bytes.Equal(previous, after) {
		t.Fatal("failed re-pair replaced prior identity")
	}
	if _, err := f2.admin.RevokeCollector(ctx, &lgmonv1.RevokeCollectorRequest{Id: "same-collector"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Join(ctx, JoinOptions{Address: "monitor.test:7443", CollectorID: "same-collector", Hostname: "FakeHost", Code: f2.code(t, time.Minute), Fingerprint: f2.authority.Fingerprint(), Keys: keys, State: state, Connector: f2.connector, Now: f2.now}); err != nil {
		t.Fatalf("same CA re-pair: %v", err)
	}
	for _, name := range []string{"../escape", "default", "x/y"} {
		if _, err := MonitorDataDirectory(home, name); err == nil {
			t.Fatal("unsafe data path")
		}
		if err := (FileState{Home: home, Monitor: name}).Save(ctx, "collector", previous, true); err == nil {
			t.Fatal("unsafe state path")
		}
		if err := (FileTargetCache{Home: home, Monitor: name}).Save(ctx, "version", []byte(`{}`)); err == nil {
			t.Fatal("unsafe config path")
		}
	}
	directory, _ := MonitorDataDirectory(home, "company")
	if directory != filepath.Join(home, "data", "monitors", "company") {
		t.Fatal(directory)
	}
	if _, err := os.Stat(filepath.Join(home, "data", "pki", "collector.json")); err != nil {
		t.Fatal("default path compatibility", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := state.Load(cancelled, "collector"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNamedMonitorQueueAckEvictionAndRestartAreIndependent(t *testing.T) {
	ctx, home := context.Background(), t.TempDir()
	clocks := []time.Time{time.UnixMilli(100000), time.UnixMilli(100000)}
	boxes := make([]*Outbox, 2)
	options := make([]OutboxOptions, 2)
	for index, name := range []string{"", "company"} {
		directory, err := MonitorDataDirectory(home, name)
		if err != nil {
			t.Fatal(err)
		}
		options[index] = OutboxOptions{Path: filepath.Join(directory, "outbox.db"), CollectorID: "fake-collector", Now: func() time.Time { return clocks[index] }, Locker: fakeCacheLocker{}, MaxBytes: MinimumCacheBytes}
		boxes[index], err = OpenOutbox(ctx, options[index])
		if err != nil {
			t.Fatal(err)
		}
		for range 3 {
			if err := boxes[index].Write(ctx, wireMessage(1)); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() {
		for _, box := range boxes {
			box.Close()
		}
	})
	if err := boxes[0].Acknowledge(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if state, err := boxes[1].State(ctx); err != nil || state.Acked != 0 || state.Allocated != 3 {
		t.Fatal("another monitor cleared the queue", state, err)
	}
	clocks[1] = clocks[1].Add(25 * time.Hour)
	if err := boxes[1].Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if err := boxes[1].Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	boxes[1], err = OpenOutbox(ctx, options[1])
	if err != nil {
		t.Fatal(err)
	}
	gap, ok, err := boxes[1].Next(ctx)
	if err != nil || !ok || gap.Kind != pipeline.KindDataGap || gap.DataGap.Dropped != 3 {
		t.Fatal("private gap did not survive restart", gap, err)
	}
	if state, err := boxes[0].State(ctx); err != nil || state.Acked != 3 || state.GapFrom != 0 {
		t.Fatal("other monitor inherited eviction", state, err)
	}
	if err := boxes[0].Write(ctx, wireMessage(1)); err != nil {
		t.Fatal(err)
	}
	if state, err := boxes[0].State(ctx); err != nil || state.Allocated != 4 || state.Acked != 3 {
		t.Fatal("sequence changed across monitors", state, err)
	}
}
