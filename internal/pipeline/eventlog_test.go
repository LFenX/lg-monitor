package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
)

type eventLogSink struct {
	messages []Message
	fail     bool
}

func (s *eventLogSink) Write(_ context.Context, m Message) error {
	s.messages = append(s.messages, m)
	return nil
}
func (*eventLogSink) EventLogCheckpointState(context.Context, string) (EventLogCheckpoint, bool, error) {
	return EventLogCheckpoint{}, false, nil
}
func (s *eventLogSink) WriteEventLogCheckpoint(_ context.Context, messages []Message, _ EventLogCheckpoint) error {
	if s.fail {
		return errors.New("fake commit failure")
	}
	s.messages = append(s.messages, messages...)
	return nil
}

func TestEventLogEmitterSequenceAndOwnershipAcrossFailedCheckpoint(t *testing.T) {
	ctx := context.Background()
	sink := &eventLogSink{fail: true}
	emitter, err := NewEmitter("fake", sink, func() time.Time { return time.UnixMilli(1000) })
	if err != nil {
		t.Fatal(err)
	}
	record := collector.EventLogRecord{Channel: "Application", Provider: "Application Error", EventID: 1000, Level: 2, RecordID: 1, TS: 1000, Presets: []string{"app_crash"}, Targets: []collector.EventLogTarget{{ID: "app", Via: "both"}}}
	message := Message{Kind: KindEventLog, TS: 1000, EventLog: &record}
	checkpoint := EventLogCheckpoint{Channel: "Application", Bookmark: "fake"}
	if err := emitter.EmitEventLogCheckpoint(ctx, []Message{message}, checkpoint); err == nil {
		t.Fatal("failed checkpoint accepted")
	}
	sink.fail = false
	if err := emitter.EmitEventLogCheckpoint(ctx, nil, checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := emitter.EmitEventLogCheckpoint(ctx, []Message{message}, checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := emitter.Emit(ctx, Message{Kind: KindEventLogNotice, EventLogNotice: &collector.EventLogNotice{Kind: "eventlog_gap", Channel: "System", Message: "fake"}}); err != nil {
		t.Fatal(err)
	}
	record.Presets[0], record.Targets[0].ID = "changed", "changed"
	if len(sink.messages) != 2 || sink.messages[0].Seq != 1 || sink.messages[1].Seq != 2 || sink.messages[0].EventLog.Presets[0] != "app_crash" || sink.messages[0].EventLog.Targets[0].ID != "app" {
		t.Fatalf("sequence or ownership lost: %+v", sink.messages)
	}
}

func TestEventLogCheckpointPreservesValidLongTargetIDs(t *testing.T) {
	id := strings.Repeat("a", 4096)
	checkpoint := EventLogCheckpoint{Channel: "Application", Bookmark: "fake", Fences: map[string]EventLogFence{id: {Signature: strings.Repeat("0", 64), AfterID: 1}}}
	if err := checkpoint.Validate(); err != nil {
		t.Fatalf("existing target identifier semantics were restricted: %v", err)
	}
	checkpoint.Fences = map[string]EventLogFence{"invalid\x00id": {Signature: strings.Repeat("0", 64)}}
	if err := checkpoint.Validate(); err == nil {
		t.Fatal("invalid checkpoint ID accepted")
	}
}
