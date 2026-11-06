package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestCommandLineUsedForMatchingNeverEntersPipelineOrDatabase(t *testing.T) {
	f := newFixture(t, "targets: [{id: web, match: {process: demo.exe, cmdline: 'password=s3cret'}}]\n")
	path := filepath.Join(t.TempDir(), "monitor.db")
	db, err := store.Open(context.Background(), store.Options{Path: path, Now: f.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	cancel, done := startCollector(t, f.options)
	<-f.clock.created
	receive(t, f.processes.requests).reply <- snapshotReply{rows: []collector.Process{{PID: 42, ParentPID: 7, Name: "demo.exe", Exe: `C:\fake\demo.exe`, CreatedAt: 1, Cmdline: "demo.exe --password=s3cret", CPUPercent: 12.5, RSS: 4096, Threads: 3}}}
	for i, kind := range []pipeline.Kind{pipeline.KindTargetStatus, pipeline.KindProcessSample} {
		msg := message(t, f.channel, uint64(i+1), kind)
		encoded, err := json.Marshal(msg)
		if err != nil || bytes.Contains(encoded, []byte("s3cret")) || bytes.Contains(encoded, []byte("cmdline")) {
			t.Fatalf("pipeline exposed command line: %s %v", encoded, err)
		}
		if kind == pipeline.KindTargetStatus && (msg.TargetStatus.State != collector.StateUp || len(msg.TargetStatus.Processes) != 1 || msg.TargetStatus.Processes[0].Threads != 3) {
			t.Fatal("local command matching or nonsecret process facts were lost")
		}
		if err := db.Write(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		data, err := os.ReadFile(name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("s3cret")) || bytes.Contains(data, []byte("cmdline")) {
			t.Fatalf("persisted bytes exposed command line in %s", filepath.Base(name))
		}
	}
}
