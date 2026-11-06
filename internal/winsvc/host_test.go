package winsvc

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recordingReporter struct{ statuses chan RuntimeStatus }

func (r recordingReporter) Report(status RuntimeStatus) error { r.statuses <- status; return nil }

type recordingEvents struct {
	mu     sync.Mutex
	errors []string
}

func (*recordingEvents) Info(string) error { return nil }
func (r *recordingEvents) Error(message string) error {
	r.mu.Lock()
	r.errors = append(r.errors, message)
	r.mu.Unlock()
	return nil
}

type testHostTicker struct{ ticks chan time.Time }

func (t *testHostTicker) Ticks() <-chan time.Time { return t.ticks }
func (*testHostTicker) Stop()                     {}

type testHostClock struct {
	ticker *testHostTicker
	millis atomic.Int64
}

func (c *testHostClock) Now() time.Time                 { return time.UnixMilli(c.millis.Load()) }
func (c *testHostClock) NewTicker(time.Duration) Ticker { return c.ticker }

func receiveState(t *testing.T, statuses <-chan RuntimeStatus, expected State) RuntimeStatus {
	t.Helper()
	select {
	case state := <-statuses:
		if state.State != expected {
			t.Fatalf("status %+v want %s", state, expected)
		}
		return state
	case <-time.After(time.Second):
		t.Fatal("service did not report state")
		return RuntimeStatus{}
	}
}

func TestServiceHostReportsReadyInterrogateAndControlledShutdown(t *testing.T) {
	for _, command := range []Control{ControlStop, ControlShutdown} {
		t.Run(string(command), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requests, states := make(chan Control, 3), make(chan RuntimeStatus, 8)
			release := make(chan struct{})
			var once sync.Once
			done := make(chan error, 1)
			go func() {
				done <- Supervise(ctx, HostOptions{Requests: requests, Reporter: recordingReporter{states}, Events: &recordingEvents{}, Run: func(ctx context.Context, ready func()) error { ready(); <-ctx.Done(); <-release; return nil }})
			}()
			defer func() { once.Do(func() { close(release) }); cancel() }()
			receiveState(t, states, StartPending)
			receiveState(t, states, Running)
			requests <- ControlInterrogate
			receiveState(t, states, Running)
			requests <- command
			receiveState(t, states, StopPending)
			select {
			case <-done:
				t.Fatal("reported stopped before runner drained")
			default:
			}
			once.Do(func() { close(release) })
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("service did not finish")
			}
		})
	}
}

func TestServiceHostStartupFailureReturnsFailureForSCMRecovery(t *testing.T) {
	sentinel := errors.New("controlled startup failure")
	events := &recordingEvents{}
	err := Supervise(context.Background(), HostOptions{Reporter: recordingReporter{make(chan RuntimeStatus, 4)}, Events: events, Run: func(context.Context, func()) error { return sentinel }})
	if !errors.Is(err, sentinel) || len(events.errors) != 1 {
		t.Fatalf("startup failure was not logged/returned: %v %v", err, events.errors)
	}
}

func TestServiceHostStartupTimeoutReportsProgressAndCancelsRunner(t *testing.T) {
	clock := &testHostClock{ticker: &testHostTicker{ticks: make(chan time.Time, 1)}}
	clock.millis.Store(1000)
	states := make(chan RuntimeStatus, 8)
	runnerStopped := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Supervise(context.Background(), HostOptions{Reporter: recordingReporter{states}, Events: &recordingEvents{}, Clock: clock, Now: clock.Now, StartTimeout: 5 * time.Second, Run: func(ctx context.Context, _ func()) error { <-ctx.Done(); close(runnerStopped); return nil }})
	}()
	receiveState(t, states, StartPending)
	clock.millis.Store(3000)
	clock.ticker.ticks <- time.Time{}
	if progress := receiveState(t, states, StartPending); progress.CheckPoint != 2 {
		t.Fatalf("missing startup checkpoint: %+v", progress)
	}
	clock.millis.Store(7000)
	clock.ticker.ticks <- time.Time{}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("startup timeout did not fail")
		}
	case <-time.After(time.Second):
		t.Fatal("startup did not time out")
	}
	select {
	case <-runnerStopped:
	case <-time.After(time.Second):
		t.Fatal("timed out service runner was not canceled")
	}
}

func TestRequestedStopDeadlineDoesNotCauseFailureRecovery(t *testing.T) {
	clock := &testHostClock{ticker: &testHostTicker{ticks: make(chan time.Time, 1)}}
	clock.millis.Store(1000)
	states, requests := make(chan RuntimeStatus, 8), make(chan Control, 1)
	release, runnerDone := make(chan struct{}), make(chan struct{})
	defer func() { close(release); <-runnerDone }()
	done := make(chan error, 1)
	go func() {
		done <- Supervise(context.Background(), HostOptions{Requests: requests, Reporter: recordingReporter{states}, Events: &recordingEvents{}, Clock: clock, Now: clock.Now, StopTimeout: 5 * time.Second, Run: func(_ context.Context, ready func()) error { defer close(runnerDone); ready(); <-release; return nil }})
	}()
	receiveState(t, states, StartPending)
	receiveState(t, states, Running)
	requests <- ControlStop
	receiveState(t, states, StopPending)
	clock.millis.Store(7000)
	clock.ticker.ticks <- time.Time{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("requested stop would trigger recovery: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stop deadline was not honored")
	}
}
