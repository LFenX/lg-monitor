package transport

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
)

type processDirectoryFunc func(context.Context, collector.ProcessIdentity) (string, error)

func (f processDirectoryFunc) WorkingDirectory(ctx context.Context, id collector.ProcessIdentity) (string, error) {
	return f(ctx, id)
}

func TestRemoteDirectoryMatchingDoesNotExposeWorkingDirectoryInProcessLists(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	cache := &collector.SnapshotCache{}
	cache.UpdateProcesses(2000000000000, []collector.Process{{PID: 42, CreatedAt: 1, Name: "node.exe", Exe: `D:\tools\node.exe`}}, nil)
	var reads atomic.Int32
	source := SnapshotResponder{Snapshots: cache, Directories: processDirectoryFunc(func(context.Context, collector.ProcessIdentity) (string, error) {
		reads.Add(1)
		return `\\?\D:\sites\blog\`, nil
	})}
	writer, err := NewWriterWithProcesses(ctx, connection, Hello{CollectorID: "fake-collector", Capabilities: []string{MatchDirectoryCapability}}, nil, source)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	spec := &config.Match{Process: "node", WorkDir: `D:\sites\blog`}
	result, err := receiver.QueryProcesses(ctx, "fake-collector", spec)
	if err != nil || len(result.Processes) != 1 || result.Processes[0].WorkDir != `D:\sites\blog` || reads.Load() != 1 {
		t.Fatalf("remote directory match: %+v %v", result, err)
	}
	listed, err := receiver.QueryProcesses(ctx, "fake-collector", nil)
	if err != nil || len(listed.Processes) != 1 || listed.Processes[0].WorkDir != "" || reads.Load() != 1 {
		t.Fatalf("ordinary list exposed or read directory: %+v %v", listed, err)
	}
	encoded, _ := json.Marshal(listed)
	var public map[string]any
	_ = json.Unmarshal(encoded, &public)
	if _, leaked := public["processes"].([]any)[0].(map[string]any)["workDir"]; leaked {
		t.Fatal("ordinary process JSON advertises working directory")
	}
	if snapshot := cache.Latest(); snapshot.Processes[0].WorkDir != "" {
		t.Fatal("draft match changed the regular snapshot")
	}
}

func TestOldProcessQueryCollectorRejectsDirectoryDraftBeforeRequestingIt(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	cache := &collector.SnapshotCache{}
	cache.UpdateProcesses(2000000000000, []collector.Process{{PID: 42, CreatedAt: 1, Name: "node.exe"}}, nil)
	var reads atomic.Int32
	source := SnapshotResponder{Snapshots: cache, Directories: processDirectoryFunc(func(context.Context, collector.ProcessIdentity) (string, error) {
		reads.Add(1)
		return `D:\site`, nil
	})}
	writer, err := NewWriterWithProcesses(ctx, connection, Hello{CollectorID: "fake-collector"}, nil, source)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := receiver.QueryProcesses(ctx, "fake-collector", &config.Match{Process: "node", WorkDir: `D:\site`}); !errors.Is(err, ErrCollectorOutdated) || reads.Load() != 0 {
		t.Fatalf("old collector received unsupported draft: %v", err)
	}
	if _, err := receiver.QueryProcesses(ctx, "fake-collector", nil); err != nil || !receiver.Connected("fake-collector") {
		t.Fatalf("old collector ordinary queries affected: %v", err)
	}
	plain := source.Answer(ctx, &lgmonv1.ProcessQuery{RequestId: "plain"})
	if len(plain.Processes) != 1 || plain.Processes[0].WorkDir != "" {
		t.Fatal("plain responder exposed a working directory")
	}
}
