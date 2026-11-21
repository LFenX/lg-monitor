// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTheWriterLeavesCheckpointsToTheirOwnConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitor.db")
	clock := &testClock{created: make(chan *testTicker, 2)}
	s, err := Open(context.Background(), Options{Path: path, Clock: clock, Now: func() time.Time { return time.UnixMilli(2000000000000) }})
	if err != nil {
		t.Fatal(err)
	}
	var pages int
	if err := s.db.QueryRow("PRAGMA wal_autocheckpoint").Scan(&pages); err != nil || pages != 0 {
		t.Fatalf("writer auto-checkpoint=%d err=%v", pages, err)
	}
	// Past SQLite's own 1000-page threshold the log keeps growing: nothing copies it on the commit path.
	for i := 0; i < 20000; i++ {
		if err := s.Write(context.Background(), sampleMessage(uint64(i+1), 2000000000000+int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	grown := walSize(t, path)
	if grown < 1000*4096 {
		t.Fatalf("log was checkpointed during commits: %d bytes", grown)
	}
	if err := s.Checkpoint(context.Background(), "PASSIVE"); err != nil {
		t.Fatal(err)
	}
	if err := s.Checkpoint(context.Background(), "RESTART"); err == nil {
		t.Fatal("accepted an unknown checkpoint mode")
	}
	// Readers on other connections see everything, checkpointed or not.
	view, err := OpenView(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	view.AllowReaders(context.Background(), 3)
	result, err := view.QueryParams(context.Background(), "SELECT count(*) FROM process_samples", nil)
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != int64(20000) {
		t.Fatalf("rows=%v err=%v", result.Rows, err)
	}
	view.Close()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if size := walSize(t, path); size != 0 {
		t.Fatalf("closing left a %d-byte log", size)
	}
}

func walSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path + "-wal")
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
