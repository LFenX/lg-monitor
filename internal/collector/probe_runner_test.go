package collector

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

type fakeProbeTicker struct {
	ticks   chan time.Time
	stopped atomic.Bool
}

func (t *fakeProbeTicker) Ticks() <-chan time.Time { return t.ticks }
func (t *fakeProbeTicker) Stop()                   { t.stopped.Store(true) }

type fakeProbeClock struct {
	mu      sync.Mutex
	tickers []*fakeProbeTicker
	ready   chan struct{}
}

func (c *fakeProbeClock) NewTicker(time.Duration) ProbeTicker {
	ticker := &fakeProbeTicker{ticks: make(chan time.Time, 8)}
	c.mu.Lock()
	c.tickers = append(c.tickers, ticker)
	c.mu.Unlock()
	c.ready <- struct{}{}
	return ticker
}

type executorFunc func(context.Context, string, int, config.Probe) ProbeResult

func (f executorFunc) Execute(ctx context.Context, target string, index int, probe config.Probe) ProbeResult {
	return f(ctx, target, index, probe)
}

type sinkFunc func(context.Context, ProbeResult) error

func (f sinkFunc) WriteProbe(ctx context.Context, result ProbeResult) error { return f(ctx, result) }

func TestProbeSchedulingIsIndependentAndSerialized(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := &fakeProbeClock{ready: make(chan struct{}, 2)}
	started := make(chan int, 16)
	release := make(chan struct{})
	results := make(chan ProbeResult, 16)
	var active [2]atomic.Int32
	var overlap atomic.Bool
	executor := executorFunc(func(ctx context.Context, target string, index int, probe config.Probe) ProbeResult {
		if active[index].Add(1) != 1 {
			overlap.Store(true)
		}
		defer active[index].Add(-1)
		started <- index
		if index == 0 {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return ProbeResult{TargetID: target, Probe: index, OK: true}
	})
	sink := sinkFunc(func(ctx context.Context, result ProbeResult) error {
		select {
		case results <- result:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	probe := httpProbe("http://test.invalid/")
	probe.Timeout = "1h"
	done := make(chan error, 1)
	go func() {
		done <- RunProbes(ctx, []config.Target{{ID: "demo", Probes: []config.Probe{probe, probe}}}, executor, sink, clock)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("probe workers did not stop")
		}
	})
	for range 2 {
		select {
		case <-clock.ready:
		case <-time.After(5 * time.Second):
			t.Fatal("tickers were not created")
		}
	}
	seen := map[int]bool{}
	for len(seen) < 2 {
		select {
		case index := <-started:
			seen[index] = true
		case <-time.After(5 * time.Second):
			t.Fatal("one probe blocked another")
		}
	}
	clock.mu.Lock()
	first, second := clock.tickers[0], clock.tickers[1]
	clock.mu.Unlock()
	for range 3 {
		first.ticks <- time.Unix(1, 0)
	}
	second.ticks <- time.Unix(1, 0)
	countSecond := 0
	for countSecond < 2 {
		select {
		case result := <-results:
			if result.Probe == 1 {
				countSecond++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("independent probe stopped while another was active")
		}
	}
	if overlap.Load() || active[0].Load() != 1 {
		t.Fatal("a probe overlapped itself")
	}
	close(release)
	select {
	case <-results:
	case <-time.After(5 * time.Second):
		t.Fatal("probe result stream stopped")
	}
}

func TestProbeSinkFailureStopsWorkers(t *testing.T) {
	failure := errors.New("fake sink error")
	clock := &fakeProbeClock{ready: make(chan struct{}, 1)}
	err := RunProbes(context.Background(), []config.Target{{ID: "demo", Probes: []config.Probe{httpProbe("http://test.invalid/")}}},
		executorFunc(func(context.Context, string, int, config.Probe) ProbeResult { return ProbeResult{OK: true} }),
		sinkFunc(func(context.Context, ProbeResult) error { return failure }), clock)
	if !errors.Is(err, failure) || !clock.tickers[0].stopped.Load() {
		t.Fatalf("sink failure or cleanup lost: %v", err)
	}
}

func TestProbeScheduleRejectsBadDurations(t *testing.T) {
	for _, duration := range []config.Duration{"", "0s", "-1s", "invalid"} {
		probe := httpProbe("http://test.invalid/")
		probe.Interval = duration
		err := RunProbes(context.Background(), []config.Target{{ID: "demo", Probes: []config.Probe{probe}}}, executorFunc(nil), sinkFunc(nil), nil)
		if err == nil {
			t.Fatalf("invalid interval %q was accepted", duration)
		}
	}
}
