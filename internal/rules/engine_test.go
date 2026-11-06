package rules

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

type memoryRepository struct {
	mu      sync.Mutex
	alerts  map[string]Alert
	events  []Event
	saveErr error
}

func (r *memoryRepository) ActiveAlerts(context.Context) ([]Alert, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var active []Alert
	for _, alert := range r.alerts {
		if alert.State == StateFiring {
			active = append(active, alert)
		}
	}
	return active, nil
}
func (r *memoryRepository) SaveTransition(_ context.Context, alert Alert, event Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveErr != nil {
		return r.saveErr
	}
	r.alerts[alert.ID], r.events = alert, append(r.events, event)
	return nil
}

type notificationQueue struct{ alerts chan Alert }

func (q *notificationQueue) Enqueue(ctx context.Context, alert Alert) error {
	select {
	case q.alerts <- alert:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type fixture struct {
	engine        *Engine
	repository    *memoryRepository
	notifications *notificationQueue
	cfg           *config.Config
	now           time.Time
	seq           uint64
	ids           int
}

func newFixture(t *testing.T, delay string, ruleType string, probes int) *fixture {
	t.Helper()
	f := &fixture{repository: &memoryRepository{alerts: make(map[string]Alert)}, notifications: &notificationQueue{alerts: make(chan Alert, 100)}, now: time.UnixMilli(1000000)}
	target := config.Target{ID: "web", Name: "演示服务", Match: config.Match{Process: "demo.exe"}}
	if ruleType == "port_closed" {
		port := 3001
		target.Match.Port = &port
	}
	for i := 0; i < probes; i++ {
		target.Probes = append(target.Probes, config.Probe{Type: "http", URL: "http://fake.invalid", Interval: "5s", Timeout: "1s"})
	}
	f.cfg = &config.Config{Targets: []config.Target{target}, Rules: []config.Rule{{ID: "down", Name: "可用性", Target: "web", Type: ruleType, For: config.Duration(delay), Severity: "critical"}}}
	var err error
	f.engine, err = New(context.Background(), f.options())
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) options() Options {
	return Options{Config: f.cfg, Repository: f.repository, Notifications: f.notifications, Now: func() time.Time { return f.now }, NewID: func() string { f.ids++; return fmt.Sprintf("alert-%d", f.ids) }}
}
func (f *fixture) status(t *testing.T, state string) {
	t.Helper()
	f.seq++
	process, port := collector.ProcessFound, collector.PortListening
	if state == collector.StateDown {
		process, port = collector.ProcessMissing, collector.PortClosed
		if f.cfg.Rules[0].Type == "port_closed" {
			process = collector.ProcessFound
		}
	} else if state == collector.StateUnknown {
		process, port = collector.ProcessUnknown, collector.PortUnknown
	}
	if err := f.engine.Handle(context.Background(), pipeline.Message{CollectorID: "host-test", Seq: f.seq, TS: f.now.UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: state, Process: process, Port: port, Reason: "没有匹配进程或端口"}}); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) probe(t *testing.T, index int, ok bool) {
	t.Helper()
	f.seq++
	if err := f.engine.Handle(context.Background(), pipeline.Message{CollectorID: "host-test", Seq: f.seq, TS: f.now.UnixMilli(), Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "web", Probe: index, OK: ok, Error: "连接失败"}}); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) evaluate(t *testing.T, advance time.Duration) {
	t.Helper()
	f.now = f.now.Add(advance)
	if err := f.engine.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func assertPhase(t *testing.T, engine *Engine, phase string) {
	t.Helper()
	states := engine.Snapshot()
	if len(states) != 1 || states[0].State != phase {
		t.Fatalf("rule states = %+v, want %s", states, phase)
	}
}
func takeAlert(t *testing.T, q *notificationQueue, state string) Alert {
	t.Helper()
	select {
	case alert := <-q.alerts:
		if alert.State != state {
			t.Fatalf("notification = %+v, want %s", alert, state)
		}
		return alert
	case <-time.After(5 * time.Second):
		t.Fatal("expected notification was not queued")
		return Alert{}
	}
}
func noNotification(t *testing.T, q *notificationQueue) {
	t.Helper()
	select {
	case alert := <-q.alerts:
		t.Fatalf("unexpected notification: %+v", alert)
	default:
	}
}

func TestPendingFlapThenFiringAndRecovery(t *testing.T) {
	f := newFixture(t, "5s", "process_absent", 0)
	f.status(t, collector.StateDown)
	assertPhase(t, f.engine, StatePending)
	f.evaluate(t, 4*time.Second)
	assertPhase(t, f.engine, StatePending)
	f.status(t, collector.StateUp)
	assertPhase(t, f.engine, StateOK)
	noNotification(t, f.notifications)
	if len(f.repository.events) != 0 {
		t.Fatal("pending flap was persisted as an alert")
	}
	f.status(t, collector.StateDown)
	started := f.now.UnixMilli()
	f.evaluate(t, 5*time.Second)
	firing := takeAlert(t, f.notifications, StateFiring)
	if firing.StartedAt != started {
		t.Fatalf("alert start = %d, want first failing observation %d", firing.StartedAt, started)
	}
	f.evaluate(t, 2*time.Second)
	noNotification(t, f.notifications)
	f.status(t, collector.StateUp)
	resolved := takeAlert(t, f.notifications, StateResolved)
	if resolved.ID != firing.ID || resolved.ResolvedAt != f.now.UnixMilli() || len(f.repository.events) != 2 {
		t.Fatalf("recovery = %+v, events=%+v", resolved, f.repository.events)
	}
	f.status(t, collector.StateUp)
	noNotification(t, f.notifications)
	f.status(t, collector.StateDown)
	f.evaluate(t, 5*time.Second)
	if next := takeAlert(t, f.notifications, StateFiring); next.ID == firing.ID {
		t.Fatal("a new incident reused the closed alert")
	}
}

func TestZeroDurationAndRestartDoNotRepeatFiring(t *testing.T) {
	f := newFixture(t, "0s", "port_closed", 0)
	f.status(t, collector.StateDown)
	firing := takeAlert(t, f.notifications, StateFiring)
	assertPhase(t, f.engine, StateFiring)
	restarted, err := New(context.Background(), f.options())
	if err != nil {
		t.Fatal(err)
	}
	f.engine = restarted
	f.evaluate(t, time.Second)
	noNotification(t, f.notifications)
	f.status(t, collector.StateDown)
	noNotification(t, f.notifications)
	f.status(t, collector.StateUp)
	if resolved := takeAlert(t, f.notifications, StateResolved); resolved.ID != firing.ID {
		t.Fatal("restart lost the active alert identity")
	}
}

func TestUnknownResetsPendingAndHoldsFiring(t *testing.T) {
	f := newFixture(t, "5s", "process_absent", 0)
	f.status(t, collector.StateUnknown)
	f.evaluate(t, 20*time.Second)
	assertPhase(t, f.engine, StateOK)
	noNotification(t, f.notifications)
	f.status(t, collector.StateDown)
	f.evaluate(t, 4*time.Second)
	f.status(t, collector.StateUnknown)
	assertPhase(t, f.engine, StateOK)
	f.evaluate(t, 20*time.Second)
	f.status(t, collector.StateDown)
	assertPhase(t, f.engine, StatePending)
	f.evaluate(t, 5*time.Second)
	takeAlert(t, f.notifications, StateFiring)
	f.status(t, collector.StateUnknown)
	f.evaluate(t, 20*time.Second)
	assertPhase(t, f.engine, StateFiring)
	noNotification(t, f.notifications)
	f.status(t, collector.StateUp)
	takeAlert(t, f.notifications, StateResolved)
}

func TestProbeFailureRequiresAllProbesHealthyBeforeRecovery(t *testing.T) {
	f := newFixture(t, "0s", "probe_failed", 2)
	f.status(t, collector.StateUp)
	f.probe(t, 0, false)
	takeAlert(t, f.notifications, StateFiring)
	f.probe(t, 0, true)
	assertPhase(t, f.engine, StateFiring)
	noNotification(t, f.notifications)
	f.probe(t, 1, true)
	takeAlert(t, f.notifications, StateResolved)
	f.status(t, collector.StateUnknown)
	f.probe(t, 1, false)
	noNotification(t, f.notifications)
	f.status(t, collector.StateUp)
	takeAlert(t, f.notifications, StateFiring)
}

func TestPersistenceFailureRetriesSameIDWithoutDuplicateNotification(t *testing.T) {
	f := newFixture(t, "0s", "process_absent", 0)
	failure := errors.New("fake persistence failure")
	f.repository.saveErr = failure
	err := f.engine.Handle(context.Background(), pipeline.Message{CollectorID: "host-test", Seq: 1, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: collector.StateDown, Process: collector.ProcessMissing}})
	if !errors.Is(err, failure) || !errors.Is(f.engine.Evaluate(context.Background()), failure) {
		t.Fatalf("persistence failure was lost: %v", err)
	}
	noNotification(t, f.notifications)
	if len(f.repository.events) != 0 {
		t.Fatal("failed persistence generated an event")
	}
	if f.ids != 1 {
		t.Fatal("failed save regenerated the incident ID")
	}
	f.repository.saveErr = nil
	f.evaluate(t, time.Second)
	alert := takeAlert(t, f.notifications, StateFiring)
	if alert.ID != "alert-1" || f.ids != 1 || len(f.repository.events) != 1 || len(f.repository.alerts) != 1 {
		t.Fatal("retry created a duplicate incident or transition")
	}
	f.evaluate(t, time.Second)
	noNotification(t, f.notifications)
}

type failingNotificationQueue struct {
	err    error
	queued []Alert
}

func (q *failingNotificationQueue) Enqueue(_ context.Context, alert Alert) error {
	if q.err != nil {
		return q.err
	}
	q.queued = append(q.queued, alert)
	return nil
}

func TestFailedEnqueueRetriesBeforeRecovery(t *testing.T) {
	f := newFixture(t, "0s", "process_absent", 0)
	queue := &failingNotificationQueue{err: errors.New("controlled full queue")}
	f.engine.notifications = queue
	message := pipeline.Message{CollectorID: "host-test", Seq: 1, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: collector.StateDown, Process: collector.ProcessMissing}}
	if err := f.engine.Handle(context.Background(), message); !errors.Is(err, queue.err) {
		t.Fatalf("queue failure was hidden: %v", err)
	}
	if len(f.repository.events) != 1 || f.ids != 1 || len(queue.queued) != 0 {
		t.Fatal("enqueue failure changed the persisted transition")
	}
	message.Seq++
	message.TargetStatus.State, message.TargetStatus.Process = collector.StateUp, collector.ProcessFound
	if err := f.engine.Handle(context.Background(), message); !errors.Is(err, queue.err) {
		t.Fatalf("pending firing notice was lost: %v", err)
	}
	queue.err = nil
	f.evaluate(t, time.Second)
	if len(queue.queued) != 2 || queue.queued[0].State != StateFiring || queue.queued[1].State != StateResolved || queue.queued[0].ID != queue.queued[1].ID || len(f.repository.events) != 2 {
		t.Fatalf("notification retry reordered or duplicated transitions: %+v", queue.queued)
	}
}

type testTicker struct {
	ticks   chan time.Time
	stopped atomic.Bool
}

func (t *testTicker) Ticks() <-chan time.Time { return t.ticks }
func (t *testTicker) Stop()                   { t.stopped.Store(true) }

type testClock struct {
	ticker  *testTicker
	created chan time.Duration
}

func (c *testClock) NewTicker(interval time.Duration) collector.ProbeTicker {
	c.created <- interval
	return c.ticker
}

func TestRunEvaluatesEverySecondWithoutNewData(t *testing.T) {
	f := newFixture(t, "2s", "process_absent", 0)
	clock := &testClock{ticker: &testTicker{ticks: make(chan time.Time, 1)}, created: make(chan time.Duration, 1)}
	var millis atomic.Int64
	millis.Store(1000000)
	options := f.options()
	options.Now = func() time.Time { return time.UnixMilli(millis.Load()) }
	options.Clock = clock
	engine, err := New(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	f.engine = engine
	f.status(t, collector.StateDown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- engine.Run(ctx) }()
	if interval := <-clock.created; interval != time.Second {
		t.Fatalf("rule evaluation interval = %s", interval)
	}
	millis.Add(2000)
	clock.ticker.ticks <- time.Time{}
	takeAlert(t, f.notifications, StateFiring)
	cancel()
	select {
	case err := <-done:
		if err != nil || !clock.ticker.stopped.Load() {
			t.Fatalf("shutdown = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rule loop did not stop")
	}
}

func TestConfigurationChangesInvalidateOldMatches(t *testing.T) {
	f := newFixture(t, "5s", "process_absent", 0)
	f.status(t, collector.StateDown)
	f.evaluate(t, 4*time.Second)
	f.cfg.Targets[0].Match.Process = "changed.exe"
	if err := f.engine.UpdateConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	f.evaluate(t, 5*time.Second)
	assertPhase(t, f.engine, StateOK)
	noNotification(t, f.notifications)
	f.status(t, collector.StateDown)
	f.evaluate(t, 5*time.Second)
	takeAlert(t, f.notifications, StateFiring)
	f.cfg.Rules = nil
	if err := f.engine.UpdateConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	f.evaluate(t, 10*time.Second)
	assertPhase(t, f.engine, StateFiring)
	noNotification(t, f.notifications)
}

func TestRemovingPendingRuleClearsItsTransientState(t *testing.T) {
	f := newFixture(t, "5s", "process_absent", 0)
	f.status(t, collector.StateDown)
	f.cfg.Rules = nil
	if err := f.engine.UpdateConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	if states := f.engine.Snapshot(); len(states) != 0 {
		t.Fatalf("removed pending rule was retained: %+v", states)
	}
	noNotification(t, f.notifications)
}
