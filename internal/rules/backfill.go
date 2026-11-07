package rules

import (
	"math"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

type dataWindow struct {
	known, met bool
	since      time.Time
}

func affectsCondition(spec ruleSpec, message pipeline.Message) bool {
	if spec.rule.Type == "sql" || spec.rule.Type == "collector_offline" {
		return false
	}
	if spec.rule.Type == "metric_threshold" && !strings.HasPrefix(spec.rule.Metric, "process.") && !strings.HasPrefix(spec.rule.Metric, "script.") {
		return message.SystemMetrics != nil
	}
	target := ""
	if message.TargetStatus != nil {
		target = message.TargetStatus.TargetID
	}
	if message.ProbeResult != nil {
		target = message.ProbeResult.TargetID
	}
	if message.ProcessSample != nil {
		target = message.ProcessSample.TargetID
	}
	if target != spec.target.ID {
		return false
	}
	switch spec.rule.Type {
	case "process_absent", "port_closed", "service_stopped":
		return message.TargetStatus != nil
	case "probe_failed", "script_failed":
		return message.TargetStatus != nil || message.ProbeResult != nil
	case "metric_threshold":
		if strings.HasPrefix(spec.rule.Metric, "process.") {
			return message.TargetStatus != nil || message.ProcessSample != nil
		}
		if strings.HasPrefix(spec.rule.Metric, "script.") {
			return message.ProbeResult != nil
		}
		return message.SystemMetrics != nil
	}
	return false
}

func (e *Engine) observeBackfill(message pipeline.Message) {
	if e.replay {
		return
	}
	now := e.now()
	offset := e.clockOffset(message.CollectorID)
	at := time.UnixMilli(message.TS)
	if message.TS == 0 {
		// The emitter already treats zero as an unspecified observation time.
		at = clockOriginalTime(now, offset)
	}
	late := clockAdjustedTime(at, offset).Before(time.UnixMilli(now.UnixMilli() - 30000))
	wasLate := e.backfill[message.CollectorID]
	if !late && !wasLate {
		return
	}
	if late {
		e.backfill[message.CollectorID] = true
	}
	for _, spec := range e.specs {
		if !spec.target.AppliesTo(message.CollectorID, e.hostnames[message.CollectorID]) || !affectsCondition(spec, message) {
			continue
		}
		identity := key{spec.rule.ID, message.CollectorID, spec.target.ID, ""}
		met, known, _ := e.condition(spec, message.CollectorID)
		previous, exists := e.dataWindows[identity]
		next := dataWindow{met: met, known: known}
		if met && known {
			next.since = at
			if exists && previous.met && previous.known {
				next.since = previous.since
			} else if !exists {
				if state := e.states[identity]; state != nil && state.phase == StatePending {
					pending := state.pendingSourceAt
					if pending.IsZero() {
						pending = clockOriginalTime(state.pendingAt, offset)
					}
					if pending.Before(at) {
						next.since = pending
					}
				}
			}
		}
		e.dataWindows[identity] = next
	}
	if !late {
		for identity, window := range e.dataWindows {
			if identity.collector != message.CollectorID {
				continue
			}
			if window.known && window.met {
				e.resumeSince[identity] = window.since
			}
			delete(e.dataWindows, identity)
		}
		delete(e.backfill, message.CollectorID)
	}
}

func (e *Engine) clockOffset(id string) int64 {
	if e.replay || e.collectorClock == nil {
		return 0
	}
	return e.collectorClock.ClockOffset(id)
}

func clockAdjustedTime(at time.Time, offset int64) time.Time {
	value := at.UnixMilli()
	if offset > 0 && value > math.MaxInt64-offset {
		return time.UnixMilli(math.MaxInt64)
	}
	if offset < 0 && value < math.MinInt64-offset {
		return time.UnixMilli(math.MinInt64)
	}
	return time.UnixMilli(value + offset)
}

func clockOriginalTime(at time.Time, offset int64) time.Time {
	value := at.UnixMilli()
	if offset > 0 && value < math.MinInt64+offset {
		return time.UnixMilli(math.MinInt64)
	}
	if offset < 0 && value > math.MaxInt64+offset {
		return time.UnixMilli(math.MaxInt64)
	}
	return time.UnixMilli(value - offset)
}
