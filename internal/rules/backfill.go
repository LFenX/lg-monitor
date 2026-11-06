package rules

import (
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
	at := time.UnixMilli(message.TS)
	if message.TS == 0 {
		// The emitter already treats zero as an unspecified observation time.
		at = now
	}
	late := at.Before(now.Add(-30 * time.Second))
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
				if state := e.states[identity]; state != nil && state.phase == StatePending && state.pendingAt.Before(at) {
					next.since = state.pendingAt
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
