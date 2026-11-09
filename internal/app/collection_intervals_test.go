package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestConfiguredTargetPeriodAndIndependentPrivateProcessRefresh(t *testing.T) {
	f := newFixture(t, "targets: [{id: demo, match: {process: fake.exe}}]\n")
	f.options.Interval = 4 * time.Second
	f.options.ProcessInterval = time.Second
	f.options.Snapshots = &collector.SnapshotCache{}
	startCollector(t, f.options)
	tickers := map[time.Duration]*fakeTicker{}
	for range 2 {
		tick := receive(t, f.clock.created)
		tickers[tick.interval] = tick
	}
	if tickers[time.Second] == nil || tickers[4*time.Second] == nil {
		t.Fatal("configured intervals were ignored", tickers)
	}
	first := collector.Process{PID: 42, CreatedAt: 1, Name: "fake.exe", Cmdline: "fake.exe --password=fake-private"}
	receive(t, f.processes.requests).reply <- snapshotReply{rows: []collector.Process{first}}
	message(t, f.channel, 1, pipeline.KindTargetStatus)
	if row := message(t, f.channel, 2, pipeline.KindProcessSample); row.ProcessSample.IntervalMillis != 4000 {
		t.Fatal("sample lost configured period", row)
	}
	f.clock.tick(tickers[time.Second], time.Second)
	second := first
	second.PID = 43
	second.CreatedAt = 2
	receive(t, f.processes.requests).reply <- snapshotReply{rows: []collector.Process{second}}
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		snapshot := f.options.Snapshots.Latest()
		if len(snapshot.Processes) > 0 && snapshot.Processes[0].PID == 43 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if snapshot := f.options.Snapshots.Latest(); len(snapshot.Processes) != 1 || snapshot.Processes[0].PID != 43 || snapshot.Processes[0].Cmdline != first.Cmdline {
		t.Fatal("full private table did not refresh independently", snapshot)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if row, err := f.channel.Read(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("private process refresh emitted duplicate telemetry", row, err)
	}
	f.clock.tick(tickers[4*time.Second], 3*time.Second)
	receive(t, f.processes.requests).reply <- snapshotReply{rows: []collector.Process{second}}
	message(t, f.channel, 3, pipeline.KindTargetStatus)
	if row := message(t, f.channel, 4, pipeline.KindProcessSample); row.ProcessSample.IntervalMillis != 4000 {
		t.Fatal(row)
	}
}
