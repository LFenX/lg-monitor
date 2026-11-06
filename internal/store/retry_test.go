package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func holdWriteLock(t *testing.T, path string) func() {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`BEGIN IMMEDIATE`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	released := false
	return func() {
		if !released {
			released = true
			_, err := db.Exec(`COMMIT`)
			if err != nil {
				t.Error(err)
			}
			db.Close()
		}
	}
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !condition() {
		select {
		case <-deadline:
			t.Fatal("storage condition did not become true")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestBusyBatchRetriedAfterExternalWriteLockReleased(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "busy.db")
	s := openStore(t, Options{Path: path})
	if _, err := s.db.Exec(`PRAGMA busy_timeout=10`); err != nil {
		t.Fatal(err)
	}
	release := holdWriteLock(t, path)
	defer release()
	for i := 0; i < 501; i++ {
		if err := s.Write(ctx, sampleMessage(uint64(i+1), int64(i+1000))); err != nil {
			t.Fatal(err)
		}
	}
	waitUntil(t, func() bool { return s.Err() != nil })
	if !strings.Contains(s.Err().Error(), "SQLITE_BUSY") {
		t.Fatalf("test did not produce SQLite busy: %v", s.Err())
	}
	select {
	case <-s.done:
		t.Fatal("busy batch stopped the writer")
	default:
	}
	release()
	waitUntil(t, func() bool { return s.Flush(ctx) == nil })
	if got := count(t, s, "process_samples"); got != 501 || s.Err() != nil {
		t.Fatalf("retained batch missing after unlock: rows=%d err=%v", got, s.Err())
	}
	if err := s.Write(ctx, sampleMessage(502, 2000)); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err != nil || count(t, s, "process_samples") != 502 {
		t.Fatalf("writer did not remain usable: %v", err)
	}
}

func TestBacklogBoundDropsOldestAndRetryBackoff(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "bounded.db")
	clock := &testClock{created: make(chan *testTicker, 20)}
	var logs bytes.Buffer
	s := openStore(t, Options{Path: path, Clock: clock, MaxPending: 3, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	<-clock.created
	<-clock.created
	if _, err := s.db.Exec(`PRAGMA busy_timeout=10`); err != nil {
		t.Fatal(err)
	}
	release := holdWriteLock(t, path)
	defer release()
	for i := 1; i <= 6; i++ {
		if err := s.Write(ctx, sampleMessage(uint64(i), int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	first := <-clock.created
	if first.interval != 500*time.Millisecond {
		t.Fatalf("first retry delay: %s", first.interval)
	}
	first.ticks <- time.Time{}
	second := <-clock.created
	if second.interval != time.Second || !first.stopped.Load() {
		t.Fatal("retry delay did not grow to one second")
	}
	second.ticks <- time.Time{}
	third := <-clock.created
	if third.interval != 2*time.Second {
		t.Fatal("retry delay did not grow to two seconds")
	}
	last := third
	for _, expected := range []time.Duration{4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second} {
		last.ticks <- time.Time{}
		last = <-clock.created
		if last.interval != expected {
			t.Fatalf("retry delay=%s want=%s", last.interval, expected)
		}
	}
	waitUntil(t, func() bool { return s.dropped.Load() == 3 })
	release()
	last.ticks <- time.Time{}
	waitUntil(t, func() bool { return s.Flush(ctx) == nil })
	var minimum, maximum int64
	if err := s.db.QueryRow(`SELECT min(ts),max(ts) FROM process_samples`).Scan(&minimum, &maximum); err != nil || minimum != 4 || maximum != 6 || count(t, s, "process_samples") != 3 {
		t.Fatalf("bounded backlog kept wrong records: %d..%d %v", minimum, maximum, err)
	}
	if !strings.Contains(logs.String(), "dropped=3") {
		t.Fatal("dropped raw samples were not reported")
	}
}

func TestFailedSingleOperationDoesNotPoisonWriter(t *testing.T) {
	s := openStore(t, Options{})
	sentinel := errors.New("controlled operation failure")
	if err := s.operation(context.Background(), func(context.Context, *sql.Tx) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("single operation failure not returned: %v", err)
	}
	if err := s.Write(context.Background(), sampleMessage(1, 1000)); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(context.Background()); err != nil || s.Err() != nil || count(t, s, "process_samples") != 1 {
		t.Fatalf("single operation poisoned writer: %v", err)
	}
}
