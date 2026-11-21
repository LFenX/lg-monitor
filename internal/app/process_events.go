// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/collector/match"
	"github.com/LFenX/lg-monitor/internal/config"
)

type processEventInterest struct {
	filter collector.ProcessEventFilter
	names  map[string]bool
	pids   map[int32]bool
	active bool
}

func processEventName(name string) string {
	name = name[strings.LastIndexAny(name, `\/`)+1:]
	return strings.TrimSuffix(strings.ToLower(name), ".exe")
}

func newProcessInterest(targets []config.Target, results []match.Result) *processEventInterest {
	interest := &processEventInterest{names: make(map[string]bool), pids: make(map[int32]bool)}
	for _, target := range processTargets(targets) {
		if !target.HasMatch() {
			continue
		}
		interest.active = true
		if target.Match.Process != "" {
			interest.names[processEventName(target.Match.Process)] = true
			// The native image field is ANSI; a Unicode name cannot safely filter starts.
			for _, c := range target.Match.Process {
				if c > 127 {
					interest.filter.AllStarts = true
					break
				}
			}
		} else if target.Match.Cmdline != "" || target.Match.ExeDir != "" || target.Match.WorkDir != "" {
			// An unrelated process start cannot tell us that a listener or Windows service is ready.
			interest.filter.AllStarts = true
		}
	}
	for _, result := range results {
		for _, process := range result.Processes {
			interest.names[processEventName(process.Name)] = true
			interest.pids[process.PID] = true
		}
	}
	for name := range interest.names {
		interest.filter.Names = append(interest.filter.Names, name)
	}
	for pid := range interest.pids {
		interest.filter.PIDs = append(interest.filter.PIDs, pid)
	}
	slices.Sort(interest.filter.Names)
	slices.Sort(interest.filter.PIDs)
	return interest
}

func (i *processEventInterest) wants(event collector.ProcessEvent) bool {
	if !i.active {
		return false
	}
	switch event.Kind {
	case collector.ProcessEventsReady, collector.ProcessEventsResync:
		return true
	case collector.ProcessStarted:
		return i.filter.AllStarts || i.names[processEventName(event.Name)]
	case collector.ProcessExited:
		return i.pids[event.PID] || i.names[processEventName(event.Name)]
	}
	return false
}

type processEventMonitor struct {
	source   collector.ProcessEventSource
	clock    collector.ProbeClock
	logger   *slog.Logger
	interest atomic.Pointer[processEventInterest]
	updates  chan struct{}
	wake     chan struct{}
	done     chan struct{}
}

func newProcessEventMonitor(ctx context.Context, source collector.ProcessEventSource, clock collector.ProbeClock, logger *slog.Logger) *processEventMonitor {
	monitor := &processEventMonitor{source: source, clock: clock, logger: logger, updates: make(chan struct{}, 1), wake: make(chan struct{}, 1), done: make(chan struct{})}
	go monitor.run(ctx)
	return monitor
}

func (m *processEventMonitor) update(interest *processEventInterest) {
	m.interest.Store(interest)
	select {
	case m.updates <- struct{}{}:
	default:
	}
}

func (m *processEventMonitor) wants(event collector.ProcessEvent) bool {
	interest := m.interest.Load()
	return interest != nil && interest.wants(event)
}

func (m *processEventMonitor) receive(event collector.ProcessEvent) {
	if !m.wants(event) {
		return
	}
	if event.Kind == collector.ProcessEventsReady {
		m.logger.Info("Process event source ready", "provider", "etw")
	}
	if event.Kind == collector.ProcessEventsResync {
		m.logger.Warn("Process event source lost events; reconciling current facts")
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *processEventMonitor) run(ctx context.Context) {
	defer close(m.done)
	retry := m.clock.NewTicker(10 * time.Second)
	defer retry.Stop()
	live, liveSupported := m.source.(collector.LiveProcessEventSource)
	var current *processEventInterest
	var cancel context.CancelFunc
	var sourceDone chan error
	terminal := false
	checkCleanup := func(err error) {
		var cleanup *collector.ProcessEventCleanupError
		if errors.As(err, &cleanup) {
			terminal = true
			m.logger.Error("Process event cleanup unconfirmed; automatic retry disabled", "error", err)
		}
	}
	stop := func() {
		if cancel != nil {
			cancel()
			cancel = nil
		}
		if sourceDone != nil {
			checkCleanup(<-sourceDone)
			sourceDone = nil
		}
	}
	defer stop()
	start := func() {
		if terminal || current == nil || !current.active || ctx.Err() != nil {
			return
		}
		work, cancelWork := context.WithCancel(ctx)
		cancel = cancelWork
		done := make(chan error, 1)
		sourceDone = done
		filter := current.filter
		go func() {
			if liveSupported {
				done <- live.RunWithInterest(work, m.wants, m.receive)
			} else {
				done <- m.source.Run(work, filter, m.receive)
			}
		}()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.updates:
			next := m.interest.Load()
			if current != nil && current.active == next.active && (liveSupported || reflect.DeepEqual(current.filter, next.filter)) {
				current = next
				continue
			}
			stop()
			current = next
			start()
		case err := <-sourceDone:
			checkCleanup(err)
			if cancel != nil {
				cancel()
				cancel = nil
			}
			sourceDone = nil
			if ctx.Err() == nil {
				m.logger.Warn("Process event source unavailable; using interval sampling", "error", err)
			}
		case <-retry.Ticks():
			if sourceDone == nil {
				start()
			}
		}
	}
}
