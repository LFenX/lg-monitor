package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func processLink(t *testing.T, now time.Time) (*transport.Receiver, *collector.SnapshotCache) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cache := &collector.SnapshotCache{}
	cache.UpdateProcesses(now.UnixMilli(), []collector.Process{
		{PID: 1234, ParentPID: 880, Name: "python.exe", Exe: `C:\Python\python.exe`, CreatedAt: 1000, CPUPercent: 0.4, RSS: 52428800, Threads: 6, Cmdline: "python blog.py --password=s3cret"},
		{PID: 1235, ParentPID: 880, Name: "PYTHON.EXE", Exe: `C:\Python\python.exe`, CreatedAt: 1001, Threads: 1, Cmdline: "python worker.py --password=s3cret"},
	}, []collector.Port{{PID: 1234, Port: 8080, LocalAddress: "0.0.0.0"}})
	cache.UpdateServices([]collector.Service{{Name: "fake-blog", PID: 1234, State: collector.ServiceRunning, StartType: "manual"}})
	listener := bufconn.Listen(2 * transport.MaxMessageSize)
	receiver := transport.NewReceiver(nil)
	server := transport.NewServer(receiver)
	go server.Serve(listener)
	connection, err := grpc.NewClient("passthrough:///fake-process-link", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		server.Stop()
		listener.Close()
		cancel()
		t.Fatal(err)
	}
	writer, err := transport.NewWriterWithProcesses(ctx, connection, transport.Hello{CollectorID: "fake-host", Hostname: "虚构主机"}, nil, transport.SnapshotResponder{Snapshots: cache})
	if err != nil {
		connection.Close()
		server.Stop()
		listener.Close()
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		writer.Close()
		connection.Close()
		server.Stop()
		listener.Close()
		receiver.Close()
		cancel()
	})
	return receiver, cache
}

type processSourceFunc func(context.Context, string, *config.Match) (collector.ProcessList, error)

func (f processSourceFunc) QueryProcesses(ctx context.Context, id string, spec *config.Match) (collector.ProcessList, error) {
	return f(ctx, id, spec)
}

func TestHTTPProcessesMapOfflineOutdatedAndTimeout(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{transport.ErrCollectorOffline, 503, "collector_offline"},
		{transport.ErrCollectorOutdated, 409, "collector_outdated"},
		{context.DeadlineExceeded, 504, "collector_timeout"},
	} {
		t.Run(test.code, func(t *testing.T) {
			f := newAPIFixtureWithProcessSource(t, time.UnixMilli(2000000000000), nil, processSourceFunc(func(context.Context, string, *config.Match) (collector.ProcessList, error) {
				return collector.ProcessList{}, test.err
			}))
			response := f.request(t, "GET", "/api/v1/collectors/fake-host/processes", "read", "", test.status)
			if !strings.Contains(response.Body.String(), test.code) {
				t.Fatal(response.Body.String())
			}
		})
	}
}

func TestHTTPProcessesReuseSnapshotKeepCommandsPrivateAndShareRateLimit(t *testing.T) {
	now := time.UnixMilli(2000000000000)
	processes, cache := processLink(t, now)
	f := newAPIFixtureWithProcessSource(t, now, nil, processes)
	counts := func() map[string]string {
		t.Helper()
		schema, err := f.sqlView.Schema(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		result := map[string]string{}
		for _, table := range schema.Tables {
			if table.Kind != "table" {
				continue
			}
			rows, err := f.sqlView.Query(context.Background(), fmt.Sprintf("SELECT count(*) FROM %q", table.Name))
			if err != nil {
				t.Fatal(err)
			}
			result[table.Name] = fmt.Sprint(rows.Rows[0][0])
		}
		return result
	}
	before := counts()
	body := f.request(t, "GET", "/api/v1/collectors/fake-host/processes?name=PYT", "read", "", 200).Body.String()
	var result collector.ProcessList
	if err := json.Unmarshal([]byte(body), &result); err != nil || len(result.Processes) != 2 || result.CollectedAt != now.UnixMilli() || result.Collector.ID != "fake-host" {
		t.Fatalf("result=%s %v", body, err)
	}
	f.request(t, "POST", "/api/v1/collectors/fake-host/processes/match", "config", `{"match":{"process":"python"}}`, 429)
	f.request(t, "POST", "/api/v1/collectors/fake-host/processes/match", "read", `{"match":{"cmdline":"blog.py"}}`, 403)
	f.now = f.now.Add(2 * time.Second)
	matched := f.request(t, "POST", "/api/v1/collectors/fake-host/processes/match", "config", `{"match":{"process":"python","cmdline":"blog.py"}}`, 200).Body.String()
	if err := json.Unmarshal([]byte(matched), &result); err != nil || len(result.Processes) != 1 || result.Processes[0].PID != 1234 || result.Reason == nil {
		t.Fatalf("match=%s %v", matched, err)
	}
	for _, response := range []string{body, matched, f.logs.String()} {
		if strings.Contains(response, "s3cret") || strings.Contains(response, `"cmdline"`) {
			t.Fatal("raw command line escaped collector")
		}
	}
	original := cache.Latest()
	original.Processes[0].CmdlineUnavailable = true
	cache.UpdateProcesses(now.UnixMilli(), original.Processes[:1], original.Ports)
	f.now = f.now.Add(2 * time.Second)
	unknown := f.request(t, "POST", "/api/v1/collectors/fake-host/processes/match", "config", `{"match":{"process":"python","cmdline":"blog.py"}}`, 200).Body.String()
	if !strings.Contains(unknown, "权限不足") {
		t.Fatal(unknown)
	}
	f.request(t, "GET", "/api/v1/collectors/missing/processes", "read", "", 404)
	f.request(t, "POST", "/api/v1/collectors/fake-host/processes/match", "config", `{"match":{"cmdline":"["}}`, 400)
	f.request(t, "POST", "/api/v1/collectors/fake-host/processes/match", "config", `{"match":{"port":0}}`, 400)
	f.request(t, "GET", "/api/v1/collectors/fake-host/processes?name=a&name=b", "read", "", 400)
	if after := counts(); !reflect.DeepEqual(before, after) || f.files.writes != 0 {
		t.Fatal("process query persisted state")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := processes.ReadBatch(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("process control message entered the record channel")
	}
}
