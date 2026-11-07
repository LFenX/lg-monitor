package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

type auxiliaryMetricSource struct {
	requests chan chan collector.SystemMetrics
}

func (s auxiliaryMetricSource) Snapshot(ctx context.Context) (collector.SystemMetrics, error) {
	reply := make(chan collector.SystemMetrics, 1)
	select {
	case s.requests <- reply:
	case <-ctx.Done():
		return collector.SystemMetrics{}, ctx.Err()
	}
	select {
	case row := <-reply:
		return row, nil
	case <-ctx.Done():
		return collector.SystemMetrics{}, ctx.Err()
	}
}

type serviceSnapshotReply struct {
	rows []collector.Service
	err  error
}
type auxiliaryServiceSource struct {
	requests chan chan serviceSnapshotReply
}

func (s auxiliaryServiceSource) Snapshot(ctx context.Context) ([]collector.Service, error) {
	reply := make(chan serviceSnapshotReply, 1)
	select {
	case s.requests <- reply:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case row := <-reply:
		return row.rows, row.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (s auxiliaryServiceSource) Lookup(context.Context, []string) ([]collector.Service, error) {
	return nil, errors.New("not used by full enumeration")
}

func TestAuxiliaryUsesIndependentIntervalsAndReportsOnlyServiceChanges(t *testing.T) {
	f := newFixture(t, "targets: []\n")
	metrics := auxiliaryMetricSource{make(chan chan collector.SystemMetrics, 2)}
	services := auxiliaryServiceSource{make(chan chan serviceSnapshotReply, 2)}
	f.options.Metrics, f.options.Services = metrics, services
	f.options.Snapshots = &collector.SnapshotCache{}
	instance, err := NewCollector(f.options)
	if err != nil {
		t.Fatal(err)
	}
	emitter, err := pipeline.NewEmitter(f.options.ID, f.channel, f.clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- instance.runAuxiliary(ctx, emitter) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("auxiliary workers did not stop")
		}
	})
	tickers := map[time.Duration]*fakeTicker{}
	for range 2 {
		ticker := receive(t, f.clock.created)
		tickers[ticker.interval] = ticker
	}
	metricTicker, serviceTicker := tickers[5*time.Second], tickers[10*time.Second]
	if metricTicker == nil || serviceTicker == nil {
		t.Fatalf("collection periods: %v", tickers)
	}
	metricReply, serviceReply := receive(t, metrics.requests), receive(t, services.requests)
	metricReply <- collector.SystemMetrics{TS: f.clock.Now().UnixMilli(), CPUPercent: 25}
	service := collector.Service{Name: "fake", State: "running", StartType: "automatic", PID: 42}
	serviceReply <- serviceSnapshotReply{rows: []collector.Service{service}}
	seen := map[pipeline.Kind]bool{}
	for range 2 {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		row, err := f.channel.Read(ctx)
		stop()
		if err != nil {
			t.Fatal(err)
		}
		seen[row.Kind] = true
	}
	if !seen[pipeline.KindSystemMetrics] || !seen[pipeline.KindServiceStatus] {
		t.Fatalf("initial collections missing: %v", seen)
	}
	f.clock.tick(metricTicker, 5*time.Second)
	receive(t, metrics.requests) <- collector.SystemMetrics{TS: f.clock.Now().UnixMilli(), CPUPercent: 50}
	message(t, f.channel, 3, pipeline.KindSystemMetrics)
	f.clock.tick(serviceTicker, 5*time.Second)
	receive(t, services.requests) <- serviceSnapshotReply{rows: []collector.Service{service}}
	f.clock.tick(serviceTicker, 10*time.Second)
	failed := receive(t, services.requests)
	snapshot := f.options.Snapshots.Latest()
	if !snapshot.ServicesKnown || len(snapshot.Services) != 1 || snapshot.Services[0].PID != 42 {
		t.Fatal("process query cache did not reuse regular C4 snapshot")
	}
	failed <- serviceSnapshotReply{err: errors.New("fake query failure")}
	f.clock.tick(serviceTicker, 10*time.Second)
	request := receive(t, services.requests)
	service.State, service.PID = "stopped", 0
	request <- serviceSnapshotReply{rows: []collector.Service{service}}
	if row := message(t, f.channel, 4, pipeline.KindServiceStatus); row.ServiceStatus.State != "stopped" {
		t.Fatalf("duplicate or failed snapshot generated a false change: %+v", row)
	}
	f.clock.tick(serviceTicker, 10*time.Second)
	receive(t, services.requests) <- serviceSnapshotReply{}
	if row := message(t, f.channel, 5, pipeline.KindServiceStatus); row.ServiceStatus.State != "deleted" {
		t.Fatalf("service deletion missing: %+v", row)
	}
}
