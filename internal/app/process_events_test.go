package app

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

type fakeProcessEventRun struct {
	filter  collector.ProcessEventFilter
	notify  func(collector.ProcessEvent)
	result  chan error
	stopped chan struct{}
}

type fakeProcessEvents struct{ runs chan *fakeProcessEventRun }

func (s *fakeProcessEvents) Run(ctx context.Context, filter collector.ProcessEventFilter, notify func(collector.ProcessEvent)) error {
	run := &fakeProcessEventRun{filter: filter, notify: notify, result: make(chan error, 1), stopped: make(chan struct{})}
	defer close(run.stopped)
	select {
	case s.runs <- run:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-run.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func processEventTickers(t *testing.T, clock *fakeClock) (regular, debounce, retry *fakeTicker) {
	t.Helper()
	for regular == nil || debounce == nil || retry == nil {
		ticker := receive(t, clock.created)
		switch ticker.interval {
		case 2 * time.Second:
			regular = ticker
		case 200 * time.Millisecond:
			debounce = ticker
		case 10 * time.Second:
			retry = ticker
		}
	}
	return
}

func eventSnapshot(t *testing.T, f *fixture, debounce *fakeTicker) snapshotRequest {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		f.clock.tick(debounce, 200*time.Millisecond)
		select {
		case request := <-f.processes.requests:
			return request
		case <-time.After(5 * time.Millisecond):
		case <-deadline.C:
			t.Fatal("event did not request a snapshot without the regular ticker")
			return snapshotRequest{}
		}
	}
}

func TestProcessEventsWakeFreshFactsWithoutPeriodicTickAndCoalesce(t *testing.T) {
	f := newFixture(t, "targets: [{id: web, match: {process: demo.exe}}]\n")
	source := &fakeProcessEvents{runs: make(chan *fakeProcessEventRun, 4)}
	f.options.ProcessEvents = source
	startCollector(t, f.options)
	regular, debounce, _ := processEventTickers(t, f.clock)
	receive(t, f.processes.requests).reply <- snapshotReply{rows: []collector.Process{{PID: 42, Name: "demo.exe", CreatedAt: 900000}}}
	if got := message(t, f.channel, 1, pipeline.KindTargetStatus); got.TargetStatus.State != collector.StateUp {
		t.Fatal(got)
	}
	message(t, f.channel, 2, pipeline.KindProcessSample)
	run := receive(t, source.runs)
	for i := 0; i < 100; i++ {
		run.notify(collector.ProcessEvent{Kind: collector.ProcessExited, PID: 42, Name: "demo.exe"})
	}
	// The regular two-second ticker is deliberately never advanced.
	request := eventSnapshot(t, f, debounce)
	request.reply <- snapshotReply{}
	got := message(t, f.channel, 3, pipeline.KindTargetStatus)
	if got.TargetStatus.State != collector.StateDown {
		t.Fatal("event did not cause an early real state", got)
	}
	if len(regular.ticks) != 0 || f.processes.maximum.Load() != 1 {
		t.Fatal("event reused a periodic tick or overlapped snapshots")
	}
}

func TestDelayedExitNeverRemovesReusedPIDWithoutSnapshot(t *testing.T) {
	f := newFixture(t, "targets: [{id: web, match: {process: demo.exe}}]\n")
	source := &fakeProcessEvents{runs: make(chan *fakeProcessEventRun, 4)}
	f.options.ProcessEvents = source
	startCollector(t, f.options)
	_, debounce, _ := processEventTickers(t, f.clock)
	receive(t, f.processes.requests).reply <- snapshotReply{rows: []collector.Process{{PID: 42, Name: "demo.exe", CreatedAt: 900000}}}
	message(t, f.channel, 1, pipeline.KindTargetStatus)
	message(t, f.channel, 2, pipeline.KindProcessSample)
	run := receive(t, source.runs)
	run.notify(collector.ProcessEvent{Kind: collector.ProcessExited, PID: 42, Name: "demo.exe", TS: 950000})
	eventSnapshot(t, f, debounce).reply <- snapshotReply{rows: []collector.Process{{PID: 42, Name: "demo.exe", CreatedAt: 999000}}}
	got := message(t, f.channel, 3, pipeline.KindTargetStatus)
	if got.TargetStatus.State != collector.StateUp || got.TargetStatus.Processes[0].CreatedAt != 999000 {
		t.Fatal("delayed exit poisoned a reused PID", got)
	}
}

type processEventLogHandler struct{ messages chan string }

func (h processEventLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h processEventLogHandler) Handle(_ context.Context, record slog.Record) error {
	select {
	case h.messages <- record.Message:
	default:
	}
	return nil
}
func (h processEventLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h processEventLogHandler) WithGroup(string) slog.Handler      { return h }

func TestProcessEventSourceFailureRetriesAndCancelsOnlyOwnSubscription(t *testing.T) {
	source := &fakeProcessEvents{runs: make(chan *fakeProcessEventRun, 4)}
	clock := &fakeClock{created: make(chan *fakeTicker, 4)}
	logs := make(chan string, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	monitor := newProcessEventMonitor(ctx, source, clock, slog.New(processEventLogHandler{logs}))
	defer func() { cancel(); receive(t, monitor.done) }()
	retry := receive(t, clock.created)
	interest := &processEventInterest{active: true, names: map[string]bool{"demo": true}, pids: map[int32]bool{42: true}, filter: collector.ProcessEventFilter{Names: []string{"demo"}, PIDs: []int32{42}}}
	monitor.update(interest)
	first := receive(t, source.runs)
	first.result <- errors.New("fake provider access denied")
	if got := receive(t, logs); got != "Process event source unavailable; using interval sampling" {
		t.Fatal(got)
	}
	clock.tick(retry, 10*time.Second)
	second := receive(t, source.runs)
	second.notify(collector.ProcessEvent{Kind: collector.ProcessEventsReady})
	receive(t, monitor.wake)
	if got := receive(t, logs); got != "Process event source ready" {
		t.Fatal(got)
	}
	monitor.update(&processEventInterest{})
	receive(t, second.stopped)
	if len(source.runs) != 0 {
		t.Fatal("empty interest reopened the source")
	}
}

func TestProcessEventInterestReplacementAndUnconfirmedCleanupNeverRetry(t *testing.T) {
	source := &fakeProcessEvents{runs: make(chan *fakeProcessEventRun, 4)}
	clock := &fakeClock{created: make(chan *fakeTicker, 4)}
	logs := make(chan string, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	monitor := newProcessEventMonitor(ctx, source, clock, slog.New(processEventLogHandler{logs}))
	defer func() { cancel(); receive(t, monitor.done) }()
	retry := receive(t, clock.created)
	firstInterest := &processEventInterest{active: true, names: map[string]bool{"old": true}, pids: map[int32]bool{42: true}, filter: collector.ProcessEventFilter{Names: []string{"old"}, PIDs: []int32{42}}}
	monitor.update(firstInterest)
	first := receive(t, source.runs)
	nextInterest := &processEventInterest{active: true, names: map[string]bool{"new": true}, pids: map[int32]bool{77: true}, filter: collector.ProcessEventFilter{Names: []string{"new"}, PIDs: []int32{77}}}
	monitor.update(nextInterest)
	second := receive(t, source.runs)
	receive(t, first.stopped)
	if second.filter.PIDs[0] != 77 || second.filter.Names[0] != "new" {
		t.Fatal("new interest was not installed", second.filter)
	}
	first.notify(collector.ProcessEvent{Kind: collector.ProcessExited, PID: 42, Name: "old.exe"})
	if len(monitor.wake) != 0 {
		t.Fatal("obsolete source woke removed interest")
	}
	second.result <- &collector.ProcessEventCleanupError{Session: "fake-owned-session", Err: errors.New("fake close failed")}
	if got := receive(t, logs); got != "Process event cleanup unconfirmed; automatic retry disabled" {
		t.Fatal(got)
	}
	if got := receive(t, logs); got != "Process event source unavailable; using interval sampling" {
		t.Fatal(got)
	}
	clock.tick(retry, 10*time.Second)
	monitor.update(firstInterest)
	clock.tick(retry, 10*time.Second)
	select {
	case <-source.runs:
		t.Fatal("unconfirmed cleanup created another subscription")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestProcessEventFailureKeepsRegularCollectionAndReadyReconciles(t *testing.T) {
	f := newFixture(t, "targets: [{id: web, match: {process: demo.exe}}]\n")
	source := &fakeProcessEvents{runs: make(chan *fakeProcessEventRun, 4)}
	logs := make(chan string, 8)
	f.options.ProcessEvents = source
	f.options.Logger = slog.New(processEventLogHandler{logs})
	startCollector(t, f.options)
	regular, debounce, retry := processEventTickers(t, f.clock)
	rows := []collector.Process{{PID: 42, Name: "demo.exe", CreatedAt: 900000}}
	receive(t, f.processes.requests).reply <- snapshotReply{rows: rows}
	message(t, f.channel, 1, pipeline.KindTargetStatus)
	message(t, f.channel, 2, pipeline.KindProcessSample)
	first := receive(t, source.runs)
	first.result <- errors.New("fake access denied")
	if got := receive(t, logs); got != "Process event source unavailable; using interval sampling" {
		t.Fatal(got)
	}
	f.clock.tick(regular, 2*time.Second)
	receive(t, f.processes.requests).reply <- snapshotReply{rows: rows}
	message(t, f.channel, 3, pipeline.KindProcessSample)
	f.clock.tick(retry, 10*time.Second)
	second := receive(t, source.runs)
	second.notify(collector.ProcessEvent{Kind: collector.ProcessEventsReady})
	eventSnapshot(t, f, debounce).reply <- snapshotReply{}
	if got := message(t, f.channel, 4, pipeline.KindTargetStatus); got.TargetStatus.State != collector.StateDown {
		t.Fatal("recovered source did not reconcile the current facts", got)
	}
}

func TestProcessEventInterestHandlesNativePathsAndUnicodeStartHints(t *testing.T) {
	interest := newProcessInterest([]config.Target{{ID: "unicode", Match: config.Match{Process: "演示.exe"}}}, nil)
	if !interest.wants(collector.ProcessEvent{Kind: collector.ProcessStarted, PID: 91, Name: "ANSI image is not UTF-8"}) {
		t.Fatal("Unicode name filtering lost a real start hint")
	}
	interest = newProcessInterest([]config.Target{{ID: "ascii", Match: config.Match{Process: "DEMO.EXE"}}}, nil)
	if !interest.wants(collector.ProcessEvent{Kind: collector.ProcessStarted, PID: 92, Name: `C:\fake\Demo.exe`}) || interest.wants(collector.ProcessEvent{Kind: collector.ProcessStarted, PID: 93, Name: "other.exe"}) {
		t.Fatal("native image path was not filtered by process name")
	}
}
