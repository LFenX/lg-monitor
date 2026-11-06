package app

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

type snapshotReply struct {
	rows []collector.Process
	err  error
}
type snapshotRequest struct{ reply chan snapshotReply }
type controlledProcesses struct {
	requests        chan snapshotRequest
	active, maximum atomic.Int32
}

func (s *controlledProcesses) Snapshot(ctx context.Context) ([]collector.Process, error) {
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for previous := s.maximum.Load(); active > previous && !s.maximum.CompareAndSwap(previous, active); previous = s.maximum.Load() {
	}
	request := snapshotRequest{reply: make(chan snapshotReply, 1)}
	select {
	case s.requests <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case result := <-request.reply:
		return result.rows, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type emptyPorts struct{}

func (emptyPorts) ListeningTCP(ctx context.Context) ([]collector.Port, error) { return nil, ctx.Err() }

type fakeTicker struct {
	interval time.Duration
	ticks    chan time.Time
	stopped  atomic.Bool
}

func (t *fakeTicker) Ticks() <-chan time.Time { return t.ticks }
func (t *fakeTicker) Stop()                   { t.stopped.Store(true) }

type fakeClock struct {
	millis  atomic.Int64
	created chan *fakeTicker
}

func (c *fakeClock) Now() time.Time { return time.UnixMilli(c.millis.Load()) }
func (c *fakeClock) NewTicker(interval time.Duration) collector.ProbeTicker {
	ticker := &fakeTicker{interval: interval, ticks: make(chan time.Time, 1)}
	c.created <- ticker
	return ticker
}
func (c *fakeClock) tick(ticker *fakeTicker, advance time.Duration) {
	c.millis.Add(advance.Milliseconds())
	select {
	case ticker.ticks <- c.Now():
	default:
	}
}

type executorFunc func(context.Context, string, int, config.Probe) collector.ProbeResult

func (f executorFunc) Execute(ctx context.Context, target string, index int, probe config.Probe) collector.ProbeResult {
	return f(ctx, target, index, probe)
}

type sinkFunc func(context.Context, pipeline.Message) error

func (f sinkFunc) Write(ctx context.Context, message pipeline.Message) error { return f(ctx, message) }

type fixture struct {
	options   CollectorOptions
	processes *controlledProcesses
	clock     *fakeClock
	channel   *pipeline.Channel
	files     fstest.MapFS
	manager   *config.Manager
}

func newFixture(t *testing.T, targets string) *fixture {
	t.Helper()
	files := fstest.MapFS{"targets.yaml": {Data: []byte(targets)}}
	manager, err := config.NewManager(config.FSLoader{FS: files}, nil)
	if err != nil {
		t.Fatal(err)
	}
	source := &controlledProcesses{requests: make(chan snapshotRequest, 20)}
	clock := &fakeClock{created: make(chan *fakeTicker, 20)}
	clock.millis.Store(1000000)
	channel := pipeline.NewChannel(100)
	t.Cleanup(func() { _ = channel.Close() })
	options := CollectorOptions{ID: "test-collector", Config: manager, Processes: source, Ports: emptyPorts{}, Sink: channel, Now: clock.Now, Clock: clock,
		Probes: executorFunc(func(_ context.Context, target string, index int, _ config.Probe) collector.ProbeResult {
			return collector.ProbeResult{TargetID: target, Probe: index, OK: true}
		})}
	return &fixture{options: options, processes: source, clock: clock, channel: channel, files: files, manager: manager}
}

func startCollector(t *testing.T, options CollectorOptions) (context.CancelFunc, <-chan error) {
	t.Helper()
	instance, err := NewCollector(options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- instance.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("collector shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("collector did not shut down")
		}
	})
	return cancel, done
}

func TestProbeFailureInterruptsAnInFlightCollection(t *testing.T) {
	f := newFixture(t, "targets: [{id: web, match: {process: demo.exe}, probes: [{type: http, url: 'http://test.invalid', interval: 5s, timeout: 1h}]}]\n")
	failure := errors.New("probe sink failure")
	started, finish := make(chan struct{}), make(chan struct{})
	f.options.Probes = executorFunc(func(ctx context.Context, target string, index int, _ config.Probe) collector.ProbeResult {
		close(started)
		select {
		case <-finish:
		case <-ctx.Done():
		}
		return collector.ProbeResult{TargetID: target, Probe: index, OK: true}
	})
	f.options.Sink = sinkFunc(func(ctx context.Context, message pipeline.Message) error {
		if message.Kind == pipeline.KindProbeResult {
			return failure
		}
		return f.channel.Write(ctx, message)
	})
	_, done := startCollector(t, f.options)
	ticker := receive(t, f.clock.created)
	receive(t, f.processes.requests).reply <- snapshotReply{}
	message(t, f.channel, 1, pipeline.KindTargetStatus)
	receive(t, started)
	f.clock.tick(ticker, 2*time.Second)
	receive(t, f.processes.requests)
	close(finish)
	if err := receive(t, done); !errors.Is(err, failure) {
		t.Fatalf("probe failure was hidden by cancellation: %v", err)
	}
	if !ticker.stopped.Load() {
		t.Fatal("collection ticker was retained after failure")
	}
}

func TestCollectorPreservesUnknownStatus(t *testing.T) {
	f := newFixture(t, "targets: [{id: service, match: {service: demo-service}}]\n")
	_, _ = startCollector(t, f.options)
	receive(t, f.processes.requests).reply <- snapshotReply{}
	got := message(t, f.channel, 1, pipeline.KindTargetStatus)
	if got.TargetStatus.State != collector.StateUnknown {
		t.Fatalf("unsupported service became a down record: %+v", got.TargetStatus)
	}
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for test event")
		var zero T
		return zero
	}
}

func message(t *testing.T, source pipeline.Source, seq uint64, kind pipeline.Kind) pipeline.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := source.Read(ctx)
	if err != nil || got.Seq != seq || got.Kind != kind || got.CollectorID != "test-collector" || got.TS < 1000000 {
		t.Fatalf("Read() = %+v, %v; want sequence %d kind %s", got, err, seq, kind)
	}
	return got
}

func TestCollectorDisappearanceRecoverySamplesAndFullSnapshot(t *testing.T) {
	f := newFixture(t, "targets: [{id: web, match: {process: demo.exe}}]\n")
	cancel, done := startCollector(t, f.options)
	ticker := receive(t, f.clock.created)
	if ticker.interval != 2*time.Second {
		t.Fatalf("interval = %s", ticker.interval)
	}
	row := collector.Process{PID: 42, CreatedAt: 1, Name: "demo.exe", RSS: 4096}
	receive(t, f.processes.requests).reply <- snapshotReply{rows: []collector.Process{row}}
	first := message(t, f.channel, 1, pipeline.KindTargetStatus)
	if first.TargetStatus.State != collector.StateUp || !first.TargetStatus.Full {
		t.Fatalf("initial state = %+v", first.TargetStatus)
	}
	message(t, f.channel, 2, pipeline.KindProcessSample)
	f.clock.tick(ticker, 2*time.Second)
	receive(t, f.processes.requests).reply <- snapshotReply{}
	down := message(t, f.channel, 3, pipeline.KindTargetStatus)
	if down.TargetStatus.State != collector.StateDown || down.TargetStatus.Full {
		t.Fatalf("disappearance = %+v", down.TargetStatus)
	}
	f.clock.tick(ticker, 2*time.Second)
	row.PID, row.CreatedAt = 52, 2
	receive(t, f.processes.requests).reply <- snapshotReply{rows: []collector.Process{row}}
	up := message(t, f.channel, 4, pipeline.KindTargetStatus)
	if up.TargetStatus.State != collector.StateUp || up.TargetStatus.TargetID != down.TargetStatus.TargetID {
		t.Fatalf("recovery = %+v", up.TargetStatus)
	}
	message(t, f.channel, 5, pipeline.KindProcessSample)
	f.clock.tick(ticker, 2*time.Second)
	row.CPUPercent, row.RSS = 40, 8192
	receive(t, f.processes.requests).reply <- snapshotReply{rows: []collector.Process{row}}
	sample := message(t, f.channel, 6, pipeline.KindProcessSample)
	if sample.ProcessSample.CPUPercent != 40 || sample.ProcessSample.RSS != 8192 {
		t.Fatalf("metrics = %+v", sample.ProcessSample)
	}
	f.clock.tick(ticker, 4*time.Second)
	receive(t, f.processes.requests).reply <- snapshotReply{rows: []collector.Process{row}}
	full := message(t, f.channel, 7, pipeline.KindTargetStatus)
	if !full.TargetStatus.Full || full.TS-first.TS != 10000 {
		t.Fatalf("periodic snapshot = %+v", full)
	}
	message(t, f.channel, 8, pipeline.KindProcessSample)
	cancel()
	if err := receive(t, done); err != nil || !ticker.stopped.Load() {
		t.Fatalf("shutdown = %v, ticker stopped=%v", err, ticker.stopped.Load())
	}
}

func TestCollectorNeverOverlapsSlowSnapshots(t *testing.T) {
	f := newFixture(t, "targets: [{id: web, match: {process: demo.exe}}]\n")
	_, _ = startCollector(t, f.options)
	ticker := receive(t, f.clock.created)
	receive(t, f.processes.requests).reply <- snapshotReply{}
	message(t, f.channel, 1, pipeline.KindTargetStatus)
	f.clock.tick(ticker, 2*time.Second)
	slow := receive(t, f.processes.requests)
	f.clock.tick(ticker, 2*time.Second)
	f.clock.tick(ticker, 2*time.Second)
	row := collector.Process{PID: 42, CreatedAt: 1, Name: "demo.exe"}
	slow.reply <- snapshotReply{rows: []collector.Process{row}}
	message(t, f.channel, 2, pipeline.KindTargetStatus)
	message(t, f.channel, 3, pipeline.KindProcessSample)
	receive(t, f.processes.requests).reply <- snapshotReply{}
	message(t, f.channel, 4, pipeline.KindTargetStatus)
	if f.processes.maximum.Load() != 1 {
		t.Fatalf("overlapping snapshot count = %d", f.processes.maximum.Load())
	}
}

type probeRequest struct {
	ctx   context.Context
	probe config.Probe
	reply chan bool
}

func TestCollectorReloadCancelsOldProbesAndForwardsNewResults(t *testing.T) {
	targets := "targets:\n  - id: web\n    match: {process: first.exe}\n    probes: [{type: http, url: 'http://first.invalid', interval: 5s, timeout: 1h}]\n"
	f := newFixture(t, targets)
	requests := make(chan probeRequest, 10)
	f.options.Probes = executorFunc(func(ctx context.Context, target string, index int, probe config.Probe) collector.ProbeResult {
		request := probeRequest{ctx: ctx, probe: probe, reply: make(chan bool, 1)}
		requests <- request
		result := collector.ProbeResult{TargetID: target, Probe: index, TS: f.clock.Now().UnixMilli()}
		select {
		case result.OK = <-request.reply:
		case <-ctx.Done():
			result.Error = "探测已取消"
		}
		return result
	})
	_, _ = startCollector(t, f.options)
	receive(t, f.clock.created)
	receive(t, f.processes.requests).reply <- snapshotReply{rows: []collector.Process{{PID: 42, CreatedAt: 1, Name: "first.exe"}}}
	message(t, f.channel, 1, pipeline.KindTargetStatus)
	message(t, f.channel, 2, pipeline.KindProcessSample)
	oldTicker := receive(t, f.clock.created)
	oldProbe := receive(t, requests)
	if oldTicker.interval != 5*time.Second || oldProbe.probe.URL != "http://first.invalid" {
		t.Fatal("initial probe configuration is incorrect")
	}
	f.files["targets.yaml"].Data = []byte("targets:\n  - id: web\n    match: {process: second.exe}\n    probes: [{type: http, url: 'http://second.invalid', interval: 7s, timeout: 1h}]\n")
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	receive(t, f.processes.requests).reply <- snapshotReply{rows: []collector.Process{{PID: 52, CreatedAt: 2, Name: "second.exe"}}}
	updated := message(t, f.channel, 3, pipeline.KindTargetStatus)
	if updated.TargetStatus.State != collector.StateUp || updated.TargetStatus.Processes[0].PID != 52 || !updated.TargetStatus.Full {
		t.Fatalf("updated matcher = %+v", updated.TargetStatus)
	}
	message(t, f.channel, 4, pipeline.KindProcessSample)
	newTicker, newProbe := receive(t, f.clock.created), receive(t, requests)
	if !oldTicker.stopped.Load() || oldProbe.ctx.Err() == nil || newTicker.interval != 7*time.Second || newProbe.probe.URL != "http://second.invalid" {
		t.Fatal("old probe was not stopped before replacement")
	}
	newProbe.reply <- true
	result := message(t, f.channel, 5, pipeline.KindProbeResult)
	if result.ProbeResult.TargetID != "web" || !result.ProbeResult.OK || result.ProbeResult.Probe != 0 {
		t.Fatalf("forwarded probe = %+v", result.ProbeResult)
	}
}

func TestCollectorPropagatesSourceAndSinkFailures(t *testing.T) {
	for _, sourceFailure := range []bool{true, false} {
		f := newFixture(t, "targets: [{id: web, match: {process: demo.exe}}]\n")
		failure := errors.New("fake adapter failure")
		if !sourceFailure {
			f.options.Sink = sinkFunc(func(context.Context, pipeline.Message) error { return failure })
		}
		_, done := startCollector(t, f.options)
		request := receive(t, f.processes.requests)
		if sourceFailure {
			request.reply <- snapshotReply{err: failure}
		} else {
			request.reply <- snapshotReply{}
		}
		if err := receive(t, done); !errors.Is(err, failure) {
			t.Fatalf("adapter error = %v", err)
		}
		_ = f.channel.Close()
		if _, err := f.channel.Read(context.Background()); !errors.Is(err, io.EOF) {
			t.Fatalf("failed source published a target state: %v", err)
		}
	}
}
