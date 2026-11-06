package store

import (
	"context"
	"testing"
	"time"
)

func TestHeartbeatUsesMonitorClockAndBacklogCannotAdvanceLastSeen(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1000000)
	s := openStore(t, Options{Now: func() time.Time { return now }})
	if err := s.RegisterCollector(ctx, CollectorRecord{ID: "collector-test", Hostname: "fake-host", FirstSeen: now.UnixMilli(), LastSeen: now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Second)
	if err := s.Heartbeat(ctx, "collector-test"); err != nil {
		t.Fatal(err)
	}
	want := now.UnixMilli()
	if err := s.EnsureCollector(ctx, CollectorRecord{ID: "collector-test", LastSeen: 1}); err != nil {
		t.Fatal(err)
	}
	for i, ts := range []int64{now.Add(-time.Hour).UnixMilli(), now.Add(24 * time.Hour).UnixMilli()} {
		if err := s.Write(ctx, sampleMessage(uint64(i+1), ts)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Ingest(ctx, sampleMessage(1, now.Add(48*time.Hour).UnixMilli())); err != nil {
		t.Fatal(err)
	}
	metadata, found, err := s.Collector(ctx, "collector-test")
	if err != nil || !found || metadata.LastSeen != want || metadata.Hostname != "fake-host" {
		t.Fatalf("backlog changed liveness: %+v %v", metadata, err)
	}
	if err := s.Heartbeat(ctx, "not-registered"); err == nil {
		t.Fatal("accepted heartbeat without Hello")
	}
}
