package rules

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

const EventLogTargetID = "@eventlog"

type EventLogWindow struct {
	CollectorID string
	TargetID    string
	Preset      string
	From, To    int64
	Threshold   int
}

type EventLogDetails struct {
	Count  int64                    `json:"count"`
	Latest collector.EventLogRecord `json:"latest"`
}

type EventLogSnapshot struct {
	Count       int64
	ThresholdTS int64
	Latest      *collector.EventLogRecord
}

type EventLogQueries interface {
	QueryEventLog(context.Context, EventLogWindow) (EventLogSnapshot, error)
}
type EventLogAlertUpdater interface {
	UpdateEventLogAlert(context.Context, Alert) error
}

func EventLogWindowMatches(event collector.EventLogRecord, window EventLogWindow) bool {
	if event.TS <= window.From || event.TS > window.To {
		return false
	}
	if window.Preset != "" && !slices.Contains(event.Presets, window.Preset) {
		return false
	}
	if window.TargetID == "" {
		return window.Preset != ""
	}
	for _, target := range event.Targets {
		if target.ID != window.TargetID {
			continue
		}
		if window.Preset != "" {
			return target.Via == "subject" || target.Via == "both"
		}
		return target.Via == "subscription" || target.Via == "both"
	}
	return false
}

type replayEventLogQueries struct{ messages []pipeline.Message }

func (s replayEventLogQueries) QueryEventLog(ctx context.Context, window EventLogWindow) (EventLogSnapshot, error) {
	var result EventLogSnapshot
	for _, message := range s.messages {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if message.CollectorID != window.CollectorID || message.EventLog == nil || !EventLogWindowMatches(*message.EventLog, window) {
			continue
		}
		result.Count++
		if result.Count == int64(window.Threshold) {
			result.ThresholdTS = message.TS
		}
		copy := *message.EventLog
		copy.Presets, copy.Targets = slices.Clone(copy.Presets), slices.Clone(copy.Targets)
		result.Latest = &copy
	}
	return result, nil
}

func (e *Engine) evaluateEventLog(ctx context.Context, spec ruleSpec, collectorID string, now time.Time) error {
	if source, ok := e.collectorSource.(CollectorCapabilities); ok && !e.replay {
		if capabilities, known := source.Capabilities(collectorID); known && !slices.Contains(capabilities, "eventlog") {
			return nil
		}
	}
	if e.eventLogs == nil {
		return fmt.Errorf("eventlog 规则需要事件历史查询源")
	}
	identity := key{rule: spec.rule.ID, collector: collectorID, target: spec.target.ID}
	state := e.states[identity]
	firing := state != nil && state.phase == StateFiring
	window, _ := spec.rule.EventLogWindow()
	until := clockOriginalTime(now, e.clockOffset(collectorID))
	request := EventLogWindow{CollectorID: collectorID, TargetID: spec.rule.Target, Preset: spec.rule.Preset, From: until.Add(-window).UnixMilli(), To: until.UnixMilli(), Threshold: spec.rule.EventLogCount()}
	if firing {
		request.Threshold = 0
	}
	snapshot, err := e.eventLogs.QueryEventLog(ctx, request)
	if err != nil {
		return fmt.Errorf("评估事件规则 %s 失败：%w", spec.rule.ID, err)
	}
	met := snapshot.Count >= int64(spec.rule.EventLogCount())
	if firing {
		met = snapshot.Count > 0
	}
	reason := ""
	var details *EventLogDetails
	if snapshot.Latest != nil {
		latest := *snapshot.Latest
		details = &EventLogDetails{Count: snapshot.Count, Latest: latest}
		reason = fmt.Sprintf("最近 %s 内 %d 条事件；%s %s #%d %s：%s", window, snapshot.Count, time.UnixMilli(latest.TS).In(now.Location()).Format("2006-01-02 15:04:05"), latest.Provider, latest.EventID, latest.Subject, eventLogExcerpt(latest.Message, 200))
	}
	if state == nil {
		state = &ruleState{phase: StateOK}
		e.states[identity] = state
	}
	if firing && met && !reflect.DeepEqual(state.alert.EventLog, details) {
		alert := state.alert
		alert.EventLog = details
		alert.Summary = fmt.Sprintf("%s：%s %s", display(spec.rule.Name, spec.rule.ID), display(spec.target.Name, spec.target.ID), reason)
		if updater, ok := e.repository.(EventLogAlertUpdater); ok {
			if err := updater.UpdateEventLogAlert(ctx, alert); err != nil {
				return err
			}
		}
		state.alert = alert
	}
	if met && !firing && snapshot.ThresholdTS > 0 {
		e.resumeSince[identity] = time.UnixMilli(snapshot.ThresholdTS)
	}
	e.eventLogEvaluating = details
	defer func() { e.eventLogEvaluating = nil }()
	return e.evaluateCondition(ctx, spec, identity, now, met, true, reason)
}

func eventLogExcerpt(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}
