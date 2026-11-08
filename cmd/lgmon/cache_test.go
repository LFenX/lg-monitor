package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
)

func TestStatusWarnsForRemoteCacheBelow24Hours(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(home, "data", "lgmon.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.RegisterCollector(ctx, store.CollectorRecord{ID: "fake-remote", Hostname: "FakeRemote", FirstSeen: 1, LastSeen: 1}); err != nil {
		t.Fatal(err)
	}
	rate := 100000000.0
	usage, err := pipeline.NewCacheUsage(1000000, 500000000, &rate)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Heartbeat(ctx, "fake-remote", &usage); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := execute([]string{"status", "--home", home, "--json"}, &out, &stderr); code != 0 {
		t.Fatalf("status: %s", stderr.String())
	}
	var status statusResult
	if err := json.Unmarshal(out.Bytes(), &status); err != nil || len(status.Collectors) != 1 || status.Collectors[0].Cache == nil {
		t.Fatalf("cache JSON: %s %v", out.String(), err)
	}
	if !strings.Contains(stderr.String(), "采集器 fake-remote：按当前数据量，断网时只能缓存约 4.9 小时") {
		t.Fatal(stderr.String())
	}
}

func TestCollectorStatusCacheWarmupAndRestartRequired(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home, "config", "settings.yaml")
	if err := os.WriteFile(settings, []byte("cache: {maxSize: 64MB}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(2000000000000)
	box, err := transport.OpenOutbox(ctx, transport.OutboxOptions{Path: filepath.Join(home, "data", "outbox.db"), CollectorID: "fake-local", MaxBytes: 64000000, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	for index := 0; index < 10; index++ {
		message := pipeline.Message{CollectorID: "fake-local", Seq: 1, TS: now.UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "fake-target", State: collector.StateDown, Process: collector.ProcessMissing, Reason: strings.Repeat("x", 1<<20)}}
		if err := box.Write(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	var out, stderr bytes.Buffer
	if code := execute([]string{"status", "--home", home, "--json"}, &out, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	var cold statusResult
	if err := json.Unmarshal(out.Bytes(), &cold); err != nil || cold.Cache == nil || cold.Cache.BytesPerHour != nil || strings.Contains(stderr.String(), "断网时只能") {
		t.Fatalf("cold status: %s %s %v", out.String(), stderr.String(), err)
	}
	now = now.Add(5 * time.Minute)
	if _, err := box.Usage(ctx); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stderr.Reset()
	if code := execute([]string{"status", "--home", home}, &out, &stderr); code != 0 || !strings.Contains(out.String(), "/ 64.0 MB") || !strings.Contains(out.String(), "本机未保存监控器数据") || strings.Contains(out.String(), "--role both") || !strings.Contains(stderr.String(), "调大 cache.maxSize") {
		t.Fatalf("collector cache warning: %s %s", out.String(), stderr.String())
	}
	if err := os.WriteFile(settings, []byte("cache: {maxSize: 128MB}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stderr.Reset()
	if code := execute([]string{"status", "--home", home}, &out, &stderr); code != 0 || !strings.Contains(stderr.String(), "cache.maxSize 已修改，重启采集器后生效") {
		t.Fatalf("changed cache config: %s %s", out.String(), stderr.String())
	}
}
