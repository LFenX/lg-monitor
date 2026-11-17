package transport

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestJobFileEventsAndPositionSurviveARestartTogether(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1794938700000)
	options := OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: "fake-collector", Now: func() time.Time { return now }, Locker: fakeCacheLocker{}}
	box, err := OpenOutbox(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := box.JobFileCheckpointState(ctx, "aics-upload"); found || err != nil {
		t.Fatalf("fresh outbox has a position: %t %v", found, err)
	}
	emitter, err := pipeline.NewEmitter("fake-collector", box, options.Now)
	if err != nil {
		t.Fatal(err)
	}
	event := pipeline.Message{Kind: pipeline.KindJobEvent, JobEvent: &collector.JobEventRecord{JobID: "aics-upload", Path: `D:\aics\events.jsonl`, Line: 1, Event: []byte(`{"event_id":"e1"}`)}}
	notice := pipeline.Message{Kind: pipeline.KindJobEvent, JobEvent: &collector.JobEventRecord{JobID: "aics-upload", Path: `D:\aics\events.jsonl`, Line: 2, Error: "第 2 行：不是合法事件"}}
	checkpoint := pipeline.JobFileCheckpoint{JobID: "aics-upload", Path: `D:\aics\events.jsonl`, Offset: 42, Line: 2, HeadLen: 3, HeadHash: strings.Repeat("ab", 32)}
	if err := emitter.EmitJobFileCheckpoint(ctx, []pipeline.Message{event, notice}, checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := box.Close(); err != nil {
		t.Fatal(err)
	}
	box, err = OpenOutbox(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	saved, found, err := box.JobFileCheckpointState(ctx, "aics-upload")
	if err != nil || !found || saved != checkpoint {
		t.Fatalf("position after restart = %+v %t %v", saved, found, err)
	}
	queued, err := box.NextBatch(ctx)
	if err != nil || len(queued) != 2 || string(queued[0].JobEvent.Event) != `{"event_id":"e1"}` || queued[1].JobEvent.Error == "" || queued[1].JobEvent.Line != 2 {
		t.Fatalf("queued = %+v %v", queued, err)
	}
	if err := box.WriteJobFileCheckpoint(ctx, nil, pipeline.JobFileCheckpoint{JobID: "aics-upload", Path: `D:\aics\events.jsonl`, Offset: -1}); err == nil {
		t.Fatal("a negative position was saved")
	}
}

func TestJobEventRecordsCrossTheWireUnchanged(t *testing.T) {
	for _, value := range []collector.JobEventRecord{
		{JobID: "aics-upload", Path: `D:\aics\events.jsonl`, Line: 7, Event: []byte(`{"event_id":"e1"}`)},
		{JobID: "aics-upload", Path: `D:\aics\events.jsonl`, Error: "读取任务事件文件失败：文件不存在"},
		{JobID: "aics-upload", Path: `D:\aics\events.jsonl`},
	} {
		message := pipeline.Message{CollectorID: "fake-collector", Seq: 3, TS: 1794938700000, Kind: pipeline.KindJobEvent, JobEvent: &value}
		record, err := Encode(message)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := Decode("fake-collector", record)
		if err != nil || decoded.JobEvent == nil || decoded.JobEvent.JobID != value.JobID || decoded.JobEvent.Line != value.Line || string(decoded.JobEvent.Event) != string(value.Event) || decoded.JobEvent.Error != value.Error || decoded.JobEvent.Path != value.Path {
			t.Fatalf("decoded %+v from %+v: %v", decoded.JobEvent, value, err)
		}
	}
	bad := pipeline.Message{CollectorID: "fake-collector", Seq: 3, TS: 1, Kind: pipeline.KindJobEvent, JobEvent: &collector.JobEventRecord{JobID: "aics-upload", Path: `D:\x`, Event: []byte(`{"event_id":"e1"}`)}}
	if _, err := Encode(bad); err == nil {
		t.Fatal("an event without a line number was encoded")
	}
}

type snapshotSource struct {
	ConfigSource
	cfg *config.Config
}

func (s snapshotSource) Snapshot() *config.Config { return s.cfg }

func TestReceiverKeepsJobEventsToTheAssignedCollector(t *testing.T) {
	file := &config.Job{Source: config.JobSource{Kind: "file", Path: `D:\aics\events.jsonl`}}
	cfg := &config.Config{Targets: []config.Target{
		{ID: "aics-upload", Collector: "fake-collector", Job: file},
		{ID: "api-job", Job: &config.Job{}},
		{ID: "by-host", Collector: "PROD-01", Job: file},
	}}
	r := &Receiver{config: ConfigDelivery{Source: snapshotSource{cfg: cfg}}}
	identity := Hello{CollectorID: "fake-collector", Hostname: "prod-01"}
	for job, want := range map[string]bool{"aics-upload": true, "by-host": true, "api-job": false, "missing": false} {
		if got := r.jobFileAssigned(identity, job); got != want {
			t.Fatalf("%s assigned = %t, want %t", job, got, want)
		}
	}
	if (&Receiver{}).jobFileAssigned(identity, "aics-upload") {
		t.Fatal("a receiver without configuration accepted a job file")
	}
	message := pipeline.Message{CollectorID: "other", Seq: 9, TS: 1794938700000, Kind: pipeline.KindJobEvent, JobEvent: &collector.JobEventRecord{JobID: "aics-upload", Path: `D:\x`, Line: 1, Event: []byte(`{}`)}}
	ignored := ignoredJobEvent(message)
	if err := ignored.Validate(); err != nil || ignored.Seq != 9 || ignored.JobEvent != nil || !strings.Contains(ignored.DataGap.Reason, "aics-upload") {
		t.Fatalf("ignored = %+v %v", ignored, err)
	}
}

func TestOldCollectorsAreToldToUpgradeForJobFiles(t *testing.T) {
	cfg := &config.Config{Targets: []config.Target{{ID: "aics-upload", Collector: "fake-collector", Job: &config.Job{Source: config.JobSource{Kind: "file", Path: `D:\aics\events.jsonl`}}}}}
	old := Hello{CollectorID: "fake-collector", Capabilities: []string{EventLogCapability}}
	if message := missingConfigCapability(cfg, old); !strings.Contains(message, JobFileCapability) {
		t.Fatalf("old collector message = %q", message)
	}
	current := Hello{CollectorID: "fake-collector", Capabilities: []string{JobFileCapability, HTTPChecksCapability}}
	if message := missingConfigCapability(cfg, current); message != "" {
		t.Fatalf("current collector message = %q", message)
	}
}
