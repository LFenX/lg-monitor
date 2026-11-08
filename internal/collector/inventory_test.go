package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeInventoryBackend struct {
	scopes  []inventoryScope
	entries []InventoryEntry
	err     error
}

func (f *fakeInventoryBackend) Scopes(context.Context) ([]inventoryScope, error) {
	return f.scopes, nil
}
func (f *fakeInventoryBackend) Read(context.Context, inventoryScope) ([]InventoryEntry, error) {
	return append([]InventoryEntry(nil), f.entries...), f.err
}
func TestInventorySourcesRetainCoverageFailuresAndValidateBoundaries(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	private := []byte("fake.exe --password=fake-private-value")
	entry := InventoryEntry{ID: "fake", Name: "虚构启动项", State: "registered", Fingerprint: inventoryFingerprint(key, private)}
	backend := &fakeInventoryBackend{scopes: []inventoryScope{{"startup", "user/fake/Run"}}, entries: []InventoryEntry{entry}}
	source := &inventorySource{backend: backend, epoch: "0123456789abcdef", seen: map[inventoryScope]bool{}}
	first, err := source.Snapshot(context.Background())
	if err != nil || len(first) != 1 || first[0].Validate() != nil || len(first[0].Entries) != 1 {
		t.Fatalf("snapshot=%+v %v", first, err)
	}
	data, _ := json.Marshal(first)
	if strings.Contains(string(data), "fake-private") || strings.Contains(string(data), "fake.exe") {
		t.Fatal("raw startup command escaped source")
	}
	if inventoryFingerprint([]byte(strings.Repeat("z", 32)), private) == entry.Fingerprint {
		t.Fatal("public hash replaced local HMAC")
	}
	backend.err = errors.New("fake-private-value in platform error")
	failed, err := source.Snapshot(context.Background())
	if err != nil || failed[0].ErrorCode != "read_failed" || len(failed[0].Entries) != 0 {
		t.Fatal("partial failure exposed entries", failed, err)
	}
	data, _ = json.Marshal(failed)
	if strings.Contains(string(data), "fake-private") {
		t.Fatal("platform error leaked")
	}
	backend.scopes = nil
	unloaded, err := source.Snapshot(context.Background())
	if err != nil || unloaded[0].ErrorCode != "source_unavailable" {
		t.Fatal("unloaded hive became an empty success", unloaded, err)
	}
	backend.scopes = []inventoryScope{{"startup", "user/fake/Run"}}
	backend.err = nil
	backend.entries = make([]InventoryEntry, MaxInventoryEntries+1)
	limited, _ := source.Snapshot(context.Background())
	if limited[0].ErrorCode != "limit_exceeded" || len(limited[0].Entries) != 0 {
		t.Fatal("truncated source accepted")
	}
	backend.entries = make([]InventoryEntry, MaxInventoryEntries)
	for i := range backend.entries {
		backend.entries[i] = entry
		backend.entries[i].ID = fmt.Sprint(i)
	}
	valid, _ := source.Snapshot(context.Background())
	if valid[0].ErrorCode != "" {
		t.Fatal("valid full inventory rejected", valid[0].ErrorCode)
	}
	for i := range backend.entries {
		backend.entries[i].ID = fmt.Sprint(i) + strings.Repeat("i", 400)
		backend.entries[i].Name = strings.Repeat("n", 256)
	}
	oversized, _ := source.Snapshot(context.Background())
	if oversized[0].ErrorCode != "limit_exceeded" || len(oversized[0].Entries) != 0 {
		t.Fatal("byte limit released a partial inventory")
	}
	for _, mutate := range []func(*InventorySnapshot){
		func(s *InventorySnapshot) { s.Entries = append(s.Entries, s.Entries[0]) },
		func(s *InventorySnapshot) { s.Entries[0].ID = "bad\x00id" },
		func(s *InventorySnapshot) { s.Entries[0].Fingerprint = "raw command" },
		func(s *InventorySnapshot) { s.ErrorCode = "read_failed" },
	} {
		copy := first[0]
		copy.Entries = append([]InventoryEntry(nil), copy.Entries...)
		mutate(&copy)
		if copy.Validate() == nil {
			t.Fatal("invalid inventory accepted", copy)
		}
	}
	if _, err := NewInventorySource([]byte("short")); err == nil {
		t.Fatal("short HMAC key accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Snapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}
