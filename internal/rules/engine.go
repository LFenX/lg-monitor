package rules

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

const (
	StateOK       = "ok"
	StatePending  = "pending"
	StateFiring   = "firing"
	StateResolved = "resolved"
)

type Alert struct {
	ID             string `json:"id"`
	RuleID         string `json:"ruleId"`
	CollectorID    string `json:"collectorId"`
	TargetID       string `json:"targetId"`
	State          string `json:"state"`
	Severity       string `json:"severity"`
	StartedAt      int64  `json:"startedAt"`
	ResolvedAt     int64  `json:"resolvedAt"`
	LastNotifiedAt int64  `json:"lastNotifiedAt"`
	Summary        string `json:"summary"`
}

type Event struct {
	AlertID string `json:"alertId"`
	TS      int64  `json:"ts"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}
type Repository interface {
	ActiveAlerts(context.Context) ([]Alert, error)
	SaveTransition(context.Context, Alert, Event) error
}
type NotificationSink interface {
	Enqueue(context.Context, Alert) error
}

type Options struct {
	Config        *config.Config
	Repository    Repository
	Notifications NotificationSink
	Now           func() time.Time
	Clock         collector.ProbeClock
	NewID         func() string
	Logger        *slog.Logger
}

type key struct{ rule, collector, target string }
type targetKey struct{ collector, target string }
type targetView struct{ state, reason, process, port string }
type probeView struct {
	ok      bool
	message string
}
type ruleSpec struct {
	rule   config.Rule
	target config.Target
	delay  time.Duration
}
type ruleState struct {
	phase     string
	pendingAt time.Time
	alert     Alert
	next      *pendingTransition
	notify    bool
}

type pendingTransition struct {
	alert Alert
	event Event
}

type Engine struct {
	mu            sync.Mutex
	repository    Repository
	notifications NotificationSink
	now           func() time.Time
	clock         collector.ProbeClock
	newID         func() string
	specs         []ruleSpec
	states        map[key]*ruleState
	collectors    map[string]bool
	targets       map[targetKey]targetView
	probes        map[targetKey]map[int]probeView
	logger        *slog.Logger
}

func New(ctx context.Context, options Options) (*Engine, error) {
	if options.Repository == nil || options.Notifications == nil {
		return nil, fmt.Errorf("规则引擎需要告警存储与通知队列")
	}
	specs, err := compile(options.Config)
	if err != nil {
		return nil, err
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Clock == nil {
		options.Clock = collector.NewProbeClock()
	}
	if options.NewID == nil {
		options.NewID = rand.Text
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	e := &Engine{repository: options.Repository, notifications: options.Notifications, now: options.Now, clock: options.Clock, newID: options.NewID, specs: specs, states: make(map[key]*ruleState), collectors: make(map[string]bool), targets: make(map[targetKey]targetView), probes: make(map[targetKey]map[int]probeView), logger: options.Logger}
	active, err := options.Repository.ActiveAlerts(ctx)
	if err != nil {
		return nil, fmt.Errorf("恢复触发中告警失败：%w", err)
	}
	for _, alert := range active {
		identity := key{alert.RuleID, alert.CollectorID, alert.TargetID}
		if alert.ID == "" || alert.RuleID == "" || alert.CollectorID == "" || alert.TargetID == "" || alert.State != StateFiring || e.states[identity] != nil {
			return nil, fmt.Errorf("存储中存在无效或重复的触发中告警，请检查告警数据")
		}
		e.states[identity] = &ruleState{phase: StateFiring, alert: alert}
		e.collectors[alert.CollectorID] = true
	}
	return e, nil
}

func compile(cfg *config.Config) ([]ruleSpec, error) {
	if cfg == nil {
		return nil, fmt.Errorf("规则配置不能为空")
	}
	targets := make(map[string]config.Target, len(cfg.Targets))
	for _, target := range cfg.Targets {
		targets[target.ID] = target
	}
	seen := make(map[string]bool)
	specs := make([]ruleSpec, 0, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		target, exists := targets[rule.Target]
		delay, err := rule.For.Parse()
		if rule.ID == "" || seen[rule.ID] || !exists || err != nil || delay < 0 {
			return nil, fmt.Errorf("规则 ID、目标引用或持续时间无效，请先运行 config validate")
		}
		switch rule.Type {
		case "process_absent":
			if target.Match.Process == "" && target.Match.Cmdline == "" {
				return nil, fmt.Errorf("process_absent 规则需要目标配置 match.process 或 match.cmdline")
			}
		case "port_closed":
			if target.Match.Port == nil {
				return nil, fmt.Errorf("port_closed 规则需要目标配置 match.port")
			}
		case "probe_failed":
			if len(target.Probes) == 0 {
				return nil, fmt.Errorf("probe_failed 规则需要目标配置 probes")
			}
		default:
			return nil, fmt.Errorf("M0 规则类型只支持 process_absent、port_closed 和 probe_failed")
		}
		rule.Notify, target.Probes, target.Tags = slices.Clone(rule.Notify), slices.Clone(target.Probes), slices.Clone(target.Tags)
		if target.Match.Port != nil {
			port := *target.Match.Port
			target.Match.Port = &port
		}
		seen[rule.ID] = true
		specs = append(specs, ruleSpec{rule: rule, target: target, delay: delay})
	}
	return specs, nil
}

func (e *Engine) UpdateConfig(cfg *config.Config) error {
	specs, err := compile(cfg)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	configured := make(map[[2]string]bool, len(specs))
	for _, spec := range specs {
		configured[[2]string{spec.rule.ID, spec.target.ID}] = true
	}
	for id, state := range e.states {
		if state.phase != StateFiring && !configured[[2]string{id.rule, id.target}] {
			delete(e.states, id)
		}
	}
	for _, next := range specs {
		for _, previous := range e.specs {
			if previous.rule.ID != next.rule.ID {
				continue
			}
			if previous.rule.Type != next.rule.Type || previous.rule.Target != next.rule.Target || previous.delay != next.delay {
				for id, state := range e.states {
					if id.rule == next.rule.ID && state.phase != StateFiring {
						delete(e.states, id)
					}
				}
			}
			if !reflect.DeepEqual(previous.target.Probes, next.target.Probes) {
				for id := range e.probes {
					if id.target == next.target.ID {
						delete(e.probes, id)
					}
				}
			}
			if !reflect.DeepEqual(previous.target.Match, next.target.Match) {
				for id := range e.targets {
					if id.target == next.target.ID {
						delete(e.targets, id)
					}
				}
			}
		}
	}
	// Removing a rule is not evidence of recovery; its persisted firing alert remains available.
	e.specs = specs
	return nil
}

func (e *Engine) Handle(ctx context.Context, message pipeline.Message) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("处理采集记录已取消：%w", err)
	}
	if err := message.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.collectors[message.CollectorID] = true
	switch message.Kind {
	case pipeline.KindTargetStatus:
		status := message.TargetStatus
		e.targets[targetKey{message.CollectorID, status.TargetID}] = targetView{status.State, status.Reason, status.Process, status.Port}
	case pipeline.KindProbeResult:
		probe := message.ProbeResult
		id := targetKey{message.CollectorID, probe.TargetID}
		if e.probes[id] == nil {
			e.probes[id] = make(map[int]probeView)
		}
		e.probes[id][probe.Probe] = probeView{probe.OK, probe.Error}
	default:
		return nil
	}
	return e.evaluate(ctx)
}

func (e *Engine) Evaluate(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.evaluate(ctx)
}

func (e *Engine) evaluate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("规则评估已取消：%w", err)
	}
	now := e.now()
	collectors := make([]string, 0, len(e.collectors))
	for id := range e.collectors {
		collectors = append(collectors, id)
	}
	sort.Strings(collectors)
	var failures []error
	for _, spec := range e.specs {
		for _, id := range collectors {
			if err := e.evaluateRule(ctx, spec, id, now); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

func (e *Engine) evaluateRule(ctx context.Context, spec ruleSpec, id string, now time.Time) error {
	identity := key{spec.rule.ID, id, spec.target.ID}
	state := e.states[identity]
	if state == nil {
		state = &ruleState{phase: StateOK}
		e.states[identity] = state
	}
	// An uncertain commit is retried before reacting to newer data, using the original ID and event.
	if err := e.finishTransition(ctx, state); err != nil {
		return err
	}
	met, known, reason := e.condition(spec, id)
	if !known || !met {
		if state.phase == StatePending {
			state.phase, state.pendingAt = StateOK, time.Time{}
		}
		if known && !met && state.phase == StateFiring {
			alert := state.alert
			alert.State, alert.ResolvedAt = StateResolved, now.UTC().UnixMilli()
			alert.Summary = fmt.Sprintf("%s：%s 已恢复", display(spec.rule.Name, spec.rule.ID), display(spec.target.Name, spec.target.ID))
			return e.transition(ctx, state, alert, now)
		}
		return nil
	}
	if state.phase == StateFiring {
		return nil
	}
	if state.phase != StatePending {
		state.phase, state.pendingAt, state.alert = StatePending, now, Alert{}
	}
	if now.Sub(state.pendingAt) < spec.delay {
		return nil
	}
	alert := Alert{ID: e.newID(), RuleID: spec.rule.ID, CollectorID: id, TargetID: spec.target.ID, State: StateFiring, Severity: spec.rule.Severity, StartedAt: state.pendingAt.UTC().UnixMilli(), Summary: fmt.Sprintf("%s：%s %s", display(spec.rule.Name, spec.rule.ID), display(spec.target.Name, spec.target.ID), reason)}
	if alert.ID == "" {
		return fmt.Errorf("告警 ID 生成失败")
	}
	return e.transition(ctx, state, alert, now)
}

func display(name, fallback string) string {
	if name != "" {
		return name
	}
	return fallback
}

func (e *Engine) transition(ctx context.Context, state *ruleState, alert Alert, at time.Time) error {
	event := Event{AlertID: alert.ID, TS: at.UTC().UnixMilli(), Kind: alert.State, Message: alert.Summary}
	state.next = &pendingTransition{alert: alert, event: event}
	return e.finishTransition(ctx, state)
}

func (e *Engine) finishTransition(ctx context.Context, state *ruleState) error {
	if state.next != nil {
		if err := e.repository.SaveTransition(ctx, state.next.alert, state.next.event); err != nil {
			return fmt.Errorf("保存告警状态失败，下次评估将重试：%w", err)
		}
		state.phase, state.alert = state.next.alert.State, state.next.alert
		state.next, state.notify = nil, true
	}
	if state.notify {
		if err := e.notifications.Enqueue(ctx, state.alert); err != nil {
			return fmt.Errorf("加入告警通知队列失败，下次评估将重试：%w", err)
		}
		state.notify = false
	}
	return nil
}

func (e *Engine) condition(spec ruleSpec, collectorID string) (met, known bool, reason string) {
	id := targetKey{collectorID, spec.target.ID}
	target, exists := e.targets[id]
	if !exists {
		return false, false, ""
	}
	switch spec.rule.Type {
	case "process_absent":
		return target.process == collector.ProcessMissing, target.process == collector.ProcessMissing || target.process == collector.ProcessFound, "进程不在"
	case "port_closed":
		if target.process == collector.ProcessUnknown || target.port == collector.PortUnknown || target.port == "" {
			return false, false, ""
		}
		return target.port == collector.PortClosed && target.process != collector.ProcessMissing, true, "端口未监听"
	}
	if target.state == collector.StateUnknown {
		return false, false, ""
	}
	allKnown := true
	for index := range spec.target.Probes {
		result, exists := e.probes[id][index]
		if !exists {
			allKnown = false
			continue
		}
		if !result.ok {
			return true, true, fmt.Sprintf("第 %d 个探测失败：%s", index+1, display(result.message, "未通过"))
		}
	}
	return false, allKnown, ""
}

func (e *Engine) Run(ctx context.Context) error {
	ticker := e.clock.NewTicker(time.Second)
	defer ticker.Stop()
	consecutiveFailures := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.Ticks():
			if err := e.Evaluate(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				consecutiveFailures++
				e.logger.Warn("Rule evaluation failed; retrying next tick", "consecutiveFailures", consecutiveFailures)
			} else {
				consecutiveFailures = 0
			}
		}
	}
}

type Status struct {
	RuleID      string `json:"ruleId"`
	CollectorID string `json:"collectorId"`
	TargetID    string `json:"targetId"`
	State       string `json:"state"`
	PendingAt   int64  `json:"pendingAt"`
	AlertID     string `json:"alertId,omitempty"`
}

func (e *Engine) Snapshot() []Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]Status, 0, len(e.states))
	for id, state := range e.states {
		status := Status{RuleID: id.rule, CollectorID: id.collector, TargetID: id.target, State: state.phase, AlertID: state.alert.ID}
		if state.phase == StatePending {
			status.PendingAt = state.pendingAt.UTC().UnixMilli()
		}
		result = append(result, status)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.CollectorID != b.CollectorID {
			return a.CollectorID < b.CollectorID
		}
		if a.RuleID != b.RuleID {
			return a.RuleID < b.RuleID
		}
		return a.TargetID < b.TargetID
	})
	return result
}
