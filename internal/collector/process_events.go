// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import (
	"context"
	"time"
)

type ProcessEventKind string

const (
	ProcessEventsReady  ProcessEventKind = "ready"
	ProcessStarted      ProcessEventKind = "started"
	ProcessExited       ProcessEventKind = "exited"
	ProcessEventsResync ProcessEventKind = "resync"
)

type ProcessEvent struct {
	Kind    ProcessEventKind
	PID     int32
	Name    string
	TS      int64
	Session string
}

type ProcessEventFilter struct {
	Names     []string
	PIDs      []int32
	AllStarts bool
}

// ProcessEventSource owns a temporary subscription until Run returns.
// Notify must return promptly; events are hints for a fresh snapshot, not authoritative PID state.
type ProcessEventSource interface {
	Run(context.Context, ProcessEventFilter, func(ProcessEvent)) error
}

// LiveProcessEventSource evaluates the current interest for each start or exit without replacing the session.
// The interest predicate reads only the kind, PID and name. Ready and resync still reach notify directly.
type LiveProcessEventSource interface {
	ProcessEventSource
	RunWithInterest(ctx context.Context, interested func(ProcessEvent) bool, notify func(ProcessEvent)) error
}

type ProcessEventCleanupError struct {
	Session string
	Err     error
}

func (e *ProcessEventCleanupError) Error() string {
	return "自有进程事件资源未确认释放（" + e.Session + "）：" + e.Err.Error()
}
func (e *ProcessEventCleanupError) Unwrap() error { return e.Err }

func NewProcessEventSource(now func() time.Time) ProcessEventSource {
	if now == nil {
		now = time.Now
	}
	return nativeProcessEvents{now: now}
}
