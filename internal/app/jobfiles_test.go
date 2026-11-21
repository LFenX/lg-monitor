// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

type fakeJobFiles struct {
	data map[string][]byte
	err  map[string]error
}

func (f *fakeJobFiles) ReadJobFile(_ context.Context, path string, offset int64, limit int) (collector.JobFileRead, error) {
	if err := f.err[path]; err != nil {
		return collector.JobFileRead{}, err
	}
	data, ok := f.data[path]
	if !ok {
		return collector.JobFileRead{}, fmt.Errorf("文件不存在：%w", fs.ErrNotExist)
	}
	read := collector.JobFileRead{Size: int64(len(data)), Head: append([]byte(nil), data[:min(len(data), collector.JobFileHeadBytes)]...)}
	if offset < int64(len(data)) {
		read.Data = append([]byte(nil), data[offset:min(int64(len(data)), offset+int64(limit))]...)
	}
	return read, nil
}

type fakeJobSink struct {
	messages    []pipeline.Message
	checkpoints map[string]pipeline.JobFileCheckpoint
}

func (s *fakeJobSink) Write(context.Context, pipeline.Message) error { return nil }

func (s *fakeJobSink) JobFileCheckpointState(_ context.Context, id string) (pipeline.JobFileCheckpoint, bool, error) {
	checkpoint, ok := s.checkpoints[id]
	return checkpoint, ok, nil
}

func (s *fakeJobSink) WriteJobFileCheckpoint(_ context.Context, messages []pipeline.Message, checkpoint pipeline.JobFileCheckpoint) error {
	s.messages = append(s.messages, messages...)
	s.checkpoints[checkpoint.JobID] = checkpoint
	return nil
}

// jobFileRun is one collector process: a restart is a new run over the same sink and files.
type jobFileRun struct {
	t       *testing.T
	c       *Collector
	emitter *pipeline.Emitter
	sink    *fakeJobSink
	states  map[string]pipeline.JobFileCheckpoint
	tails   map[string]jobTail
	now     *time.Time
}

func newJobFileRun(t *testing.T, files *fakeJobFiles, sink *fakeJobSink, now *time.Time) *jobFileRun {
	t.Helper()
	clock := func() time.Time { return *now }
	emitter, err := pipeline.NewEmitter("fake-collector", sink, clock)
	if err != nil {
		t.Fatal(err)
	}
	return &jobFileRun{t: t, c: &Collector{options: CollectorOptions{JobFiles: files, Now: clock}}, emitter: emitter, sink: sink, states: map[string]pipeline.JobFileCheckpoint{}, tails: map[string]jobTail{}, now: now}
}

func (r *jobFileRun) poll() []pipeline.Message {
	r.t.Helper()
	before := len(r.sink.messages)
	if err := r.c.readJobFile(context.Background(), r.emitter, r.sink, r.states, r.tails, "aics-upload", `D:\aics\events.jsonl`); err != nil {
		r.t.Fatal(err)
	}
	return r.sink.messages[before:]
}

func describe(messages []pipeline.Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		event := message.JobEvent
		switch {
		case len(event.Event) > 0:
			parts = append(parts, fmt.Sprintf("%d:event", event.Line))
		case event.Error != "":
			parts = append(parts, fmt.Sprintf("%d:error", event.Line))
		default:
			parts = append(parts, fmt.Sprintf("%d:ok", event.Line))
		}
	}
	return strings.Join(parts, " ")
}

func fileEvent(id string) string {
	return fmt.Sprintf(`{"event_id":"%s","attempt_id":"run-1","kind":"stage","stage":"fetch","state":"failed","ts":1794938700000,"error":"login alice@example.test password=hunter2 failed"}`, id) + "\n"
}

func TestJobFileReadsCompleteLinesAndResumesAfterRestart(t *testing.T) {
	const path = `D:\aics\events.jsonl`
	now := time.UnixMilli(1794938700000)
	files := &fakeJobFiles{data: map[string][]byte{path: []byte(fileEvent("e1") + "\n" + `{"event_id":"bad","mail_body":"hi"}` + "\n" + fileEvent("e2") + `{"event_id":"e3"`)}}
	sink := &fakeJobSink{checkpoints: map[string]pipeline.JobFileCheckpoint{}}
	run := newJobFileRun(t, files, sink, &now)
	got := run.poll()
	if describe(got) != "1:event 3:error 4:event" {
		t.Fatalf("first pass = %s", describe(got))
	}
	if body := string(got[0].JobEvent.Event); strings.Contains(body, "hunter2") || strings.Contains(body, "alice@") || !strings.Contains(body, "a***@example.test") {
		t.Fatalf("event left the collector unredacted: %s", body)
	}
	if !strings.Contains(got[1].JobEvent.Error, "第 3 行") {
		t.Fatalf("bad line message = %q", got[1].JobEvent.Error)
	}
	if describe(run.poll()) != "" {
		t.Fatal("a quiet file produced messages")
	}
	// The job finishes its line and adds another; a restarted collector picks up exactly there.
	files.data[path] = append(files.data[path], []byte(`,"attempt_id":"run-1","kind":"attempt","state":"finished","ts":1794939000000}`+"\n"+fileEvent("e4"))...)
	restarted := newJobFileRun(t, files, sink, &now)
	if got := describe(restarted.poll()); got != "5:event 6:event" {
		t.Fatalf("after restart = %s", got)
	}
	checkpoint := sink.checkpoints["aics-upload"]
	if checkpoint.Offset != int64(len(files.data[path])) || checkpoint.Line != 6 || checkpoint.Failing {
		t.Fatalf("checkpoint = %+v", checkpoint)
	}
}

func TestJobFileReportsProblemsOnceAndRereadsAReplacedFile(t *testing.T) {
	const path = `D:\aics\events.jsonl`
	now := time.UnixMilli(1794938700000)
	files := &fakeJobFiles{data: map[string][]byte{}}
	sink := &fakeJobSink{checkpoints: map[string]pipeline.JobFileCheckpoint{}}
	run := newJobFileRun(t, files, sink, &now)
	if got := run.poll(); describe(got) != "0:error" || !strings.Contains(got[0].JobEvent.Error, "文件不存在") {
		t.Fatalf("missing file = %s", describe(got))
	}
	if describe(run.poll()) != "" {
		t.Fatal("a still-missing file was reported twice")
	}
	files.data[path] = []byte(fileEvent("e1") + fileEvent("e2"))
	if got := describe(run.poll()); got != "0:ok 1:event 2:event" {
		t.Fatalf("file appeared = %s", got)
	}
	// Rotated: a new file with different contents is read from its start; repeats are dropped later by event_id.
	files.data[path] = []byte(fileEvent("e9"))
	if got := describe(run.poll()); got != "1:event" {
		t.Fatalf("replaced file = %s", got)
	}
	// Same length again but a different start is still a different file.
	files.data[path] = []byte(strings.Replace(fileEvent("e8"), "e8", "e7", 1) + fileEvent("e10"))
	if got := describe(run.poll()); got != "1:event 2:event" {
		t.Fatalf("replaced file of a longer length = %s", got)
	}
}

func TestJobFileWarnsAboutAnUnfinishedLastLineAndCapsLineErrors(t *testing.T) {
	const path = `D:\aics\events.jsonl`
	now := time.UnixMilli(1794938700000)
	bad := strings.Repeat("not json\n", 30)
	files := &fakeJobFiles{data: map[string][]byte{path: []byte(bad + strings.TrimSuffix(fileEvent("e1"), "\n"))}}
	sink := &fakeJobSink{checkpoints: map[string]pipeline.JobFileCheckpoint{}}
	run := newJobFileRun(t, files, sink, &now)
	got := run.poll()
	if len(got) != jobFileLineErrors+1 || !strings.Contains(got[len(got)-1].JobEvent.Error, "另有 10 行") {
		t.Fatalf("line errors = %d, last %+v", len(got), got[len(got)-1].JobEvent)
	}
	now = now.Add(30 * time.Second)
	if describe(run.poll()) != "" {
		t.Fatal("an unfinished line was reported while it might still be written")
	}
	now = now.Add(jobFileUnterminated)
	if got := run.poll(); describe(got) != "31:error" || !strings.Contains(got[0].JobEvent.Error, "没有以换行结束") {
		t.Fatalf("stale unfinished line = %s", describe(got))
	}
	now = now.Add(time.Hour)
	if describe(run.poll()) != "" {
		t.Fatal("the unfinished line was reported twice")
	}
	files.data[path] = append(files.data[path], '\n')
	if got := describe(run.poll()); got != "31:event" {
		t.Fatalf("finished line = %s", got)
	}
}

func TestJobFileRefusesEventsForAnotherJob(t *testing.T) {
	const path = `D:\aics\events.jsonl`
	now := time.UnixMilli(1794938700000)
	files := &fakeJobFiles{data: map[string][]byte{path: []byte(strings.Replace(fileEvent("e1"), `"event_id"`, `"job_id":"other-job","event_id"`, 1))}}
	sink := &fakeJobSink{checkpoints: map[string]pipeline.JobFileCheckpoint{}}
	got := newJobFileRun(t, files, sink, &now).poll()
	if describe(got) != "1:error" || !strings.Contains(got[0].JobEvent.Error, "other-job") {
		t.Fatalf("foreign job line = %s %+v", describe(got), got)
	}
}
