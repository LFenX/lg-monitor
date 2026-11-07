package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/collector/match"
	"github.com/LFenX/lg-monitor/internal/config"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/protobuf/proto"
)

func TestSnapshotResponderMatchesWithoutCommandsAndBoundsResults(t *testing.T) {
	cache := &collector.SnapshotCache{}
	processes := []collector.Process{
		{PID: 42, Name: "python.exe", CreatedAt: 1234, Cmdline: "python blog.py --password=s3cret"},
		{PID: 43, Name: "python.exe", CreatedAt: 1235, CmdlineUnavailable: true},
	}
	cache.UpdateProcesses(2000000000000, processes, []collector.Port{{PID: 42, Port: 8080, LocalAddress: "127.0.0.1"}})
	cache.UpdateServices([]collector.Service{{Name: "fake", PID: 42, State: collector.ServiceRunning}})
	processes[0].Cmdline = "modified"
	source := SnapshotResponder{Snapshots: cache}
	list := source.Answer(context.Background(), &lgmonv1.ProcessQuery{RequestId: "fake-list"})
	data, err := proto.Marshal(list)
	if err != nil || bytes.Contains(data, []byte("s3cret")) || len(list.Processes) != 2 || list.Processes[0].Ports[0].Port != 8080 || list.Processes[0].Services[0] != "fake" {
		t.Fatalf("list=%+v %v", list, err)
	}
	spec := config.Match{Process: "python", Cmdline: "blog.py"}
	encoded, _ := json.Marshal(spec)
	selected := source.Answer(context.Background(), &lgmonv1.ProcessQuery{RequestId: "fake-match", MatchJson: encoded})
	if len(selected.Processes) != 1 || selected.Processes[0].Process.Pid != 42 {
		t.Fatalf("match=%+v", selected)
	}
	snapshot := cache.Latest()
	snapshot.Processes = snapshot.Processes[1:]
	cache.UpdateProcesses(snapshot.CollectedAt, snapshot.Processes, snapshot.Ports)
	unknown := source.Answer(context.Background(), &lgmonv1.ProcessQuery{RequestId: "fake-unknown", MatchJson: encoded})
	matcher, _ := match.New([]config.Target{{ID: "draft", Match: spec}})
	expected := matcher.MatchWithServices(snapshot.Processes, snapshot.Ports, snapshot.Services)[0]
	if unknown.Reason != expected.Reason || len(unknown.Processes) != 0 {
		t.Fatal("query reason differs from formal target matching")
	}
	rows := make([]collector.Process, MaxProcessResults+1)
	for i := range rows {
		rows[i] = collector.Process{PID: int32(i + 1), CreatedAt: 1234, Name: "fake.exe"}
	}
	cache.UpdateProcesses(snapshot.CollectedAt, rows, nil)
	bounded := source.Answer(context.Background(), &lgmonv1.ProcessQuery{RequestId: "fake-bound"})
	if len(bounded.Processes) != MaxProcessResults || !bounded.Truncated || proto.Size(bounded) >= MaxMessageSize {
		t.Fatal("process result is not bounded")
	}
	cache.Latest().Processes[0].Name = "changed"
	if cache.Latest().Processes[0].Name != "fake.exe" {
		t.Fatal("snapshot cache shares mutable process slices")
	}
}

func TestProcessRequestsDoNotConsumeRecordSequencesAndOldCollectorsStayConnected(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	writer, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector"})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := receiver.QueryProcesses(ctx, "missing", nil); !errors.Is(err, ErrCollectorOffline) {
		t.Fatalf("offline=%v", err)
	}
	if _, err := receiver.QueryProcesses(ctx, "fake-collector", nil); !errors.Is(err, ErrCollectorOutdated) {
		t.Fatalf("old=%v", err)
	}
	done := make(chan error, 1)
	go func() { done <- writer.Write(ctx, wireMessage(1)) }()
	record, err := receiver.Read(ctx)
	if err != nil || record.Seq != 1 {
		t.Fatal("old collector disconnected or sequence advanced")
	}
	receiver.Acknowledge(record, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestProcessQueryReplyNeverEntersRecordQueueOrAdvancesSequence(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	cache := &collector.SnapshotCache{}
	cache.UpdateProcesses(2000000000000, []collector.Process{{PID: 42, CreatedAt: 1234, Name: "fake.exe", Cmdline: "--password=s3cret"}}, nil)
	writer, err := NewWriterWithProcesses(ctx, connection, Hello{CollectorID: "fake-collector"}, nil, SnapshotResponder{Snapshots: cache})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if list, err := receiver.QueryProcesses(ctx, "fake-collector", nil); err != nil || len(list.Processes) != 1 {
		t.Fatalf("control query failed: %v", err)
	}
	emptyCtx, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if _, err := receiver.ReadBatch(emptyCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("process control reply was persisted as a record")
	}
	done := make(chan error, 1)
	go func() { done <- writer.Write(ctx, wireMessage(1)) }()
	record, err := receiver.Read(ctx)
	if err != nil || record.Seq != 1 {
		t.Fatal("process query consumed a record sequence")
	}
	receiver.Acknowledge(record, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestProcessQueryTimeoutAndDisconnectAreBounded(t *testing.T) {
	_, receiver, connection, _ := linkFixture(t)
	streamCtx, stop := context.WithCancel(context.Background())
	defer stop()
	stream, err := lgmonv1.NewCollectorLinkClient(connection).Connect(streamCtx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_Hello{Hello: &lgmonv1.Hello{CollectorId: "fake-collector", Capabilities: []string{ProcessQueryCapability}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := receiver.QueryProcesses(context.Background(), "fake-collector", nil); !errors.Is(err, context.DeadlineExceeded) || time.Since(started) < 9*time.Second || time.Since(started) > 15*time.Second {
		t.Fatalf("timeout duration=%v err=%v", time.Since(started), err)
	}
	done := make(chan error, 1)
	go func() { _, err := receiver.QueryProcesses(context.Background(), "fake-collector", nil); done <- err }()
	// Draining the first timed-out request and the next one proves it is in flight.
	for range 2 {
		if message, err := stream.Recv(); err != nil || message.GetProcessQuery() == nil {
			t.Fatalf("query message=%+v %v", message, err)
		}
	}
	stop()
	select {
	case err := <-done:
		if !errors.Is(err, ErrCollectorOffline) {
			t.Fatalf("disconnect=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect retained pending query")
	}
	receiver.mu.Lock()
	defer receiver.mu.Unlock()
	for _, session := range receiver.active {
		if len(session.processResults) != 0 {
			t.Fatal("pending process requests leaked")
		}
	}
}
