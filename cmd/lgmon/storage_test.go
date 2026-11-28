// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestCLIStorageCompactStatusArchivesAndExtract(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	dbPath := filepath.Join(home, "data", "lgmon.db")
	s, err := store.Open(ctx, store.Options{
		Path:               dbPath,
		Now:                time.Now,
		DisableMaintenance: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldTS := time.Now().Add(-72 * time.Hour).UnixMilli()
	for i := 0; i < 6; i++ {
		if err := s.Write(ctx, pipeline.Message{
			CollectorID: "c-cli", Seq: uint64(i + 1), TS: oldTS + int64(i*60_000), Kind: pipeline.KindTargetStatus,
			TargetStatus: &pipeline.TargetStatus{TargetID: "cli-svc", State: "up", Reason: "ok"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "logs", "lgmon.log"), bytes.Repeat([]byte("line\n"), 600_000), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := execute([]string{"storage", "compact", "--home", home, "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("storage compact failed (%d): %s", code, stderr.String())
	}
	var compactRes struct {
		ArchivedRecords int   `json:"archivedRecords"`
		ArchiveBytes    int64 `json:"archiveBytes"`
		FreedBytes      int64 `json:"freedBytes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &compactRes); err != nil {
		t.Fatalf("decode compact output: %v (%s)", err, stdout.String())
	}
	if compactRes.ArchivedRecords != 6 || compactRes.ArchiveBytes <= 0 || compactRes.FreedBytes <= 0 {
		t.Fatalf("unexpected compact result: %+v", compactRes)
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"storage", "status", "--home", home, "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("storage status failed (%d): %s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"archiveBytes"`)) {
		t.Fatalf("unexpected status output: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"storage", "archives", "--home", home, "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("storage archives failed (%d): %s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"totalRecords":6`)) {
		t.Fatalf("unexpected archives output: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := execute([]string{"storage", "extract", "--home", home, "--table", "target_status", "--target", "cli-svc", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("storage extract failed (%d): %s", code, stderr.String())
	}
	var extracted store.ArchiveExtractResult
	if err := json.Unmarshal(stdout.Bytes(), &extracted); err != nil || extracted.Total != 6 || len(extracted.Records) != 6 {
		t.Fatalf("unexpected extract output: %+v err=%v", extracted, err)
	}
}
