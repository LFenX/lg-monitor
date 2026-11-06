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
	ID              string `json:"id"`
	RuleID          string `json:"ruleId"`
	CollectorID     string `json:"collectorId"`
	TargetID        string `json:"targetId"`
	State           string `json:"state"`
	Severity        string `json:"severity"`
	StartedAt       int64  `json:"startedAt"`
	ResolvedAt      int64  `json:"resolvedAt"`
	LastNotifiedAt  int64  `json:"lastNotifiedAt"`
	Summary         string `json:"summary"`
	InstanceKey     string `json:"instanceKey,omitempty"`
	AcknowledgedAt  int64  `json:"acknowledgedAt,omitempty"`
	NotificationKey string `json:"notificationKey,omitempty"`
	NotificationAt  int64  `json:"notificationAt,omitempty"`
	Flapping        bool   `json:"flapping,omitempty"`
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

type TransitionObserver interface{ Transition(Alert, Event) }

type Options struct {
	Config            *config.Config
	Repository        Repository
	Notifications     NotificationSink
	Now               func() time.Time
	Clock             collector.ProbeClock
	NewID             func() string
	Logger            *slog.Logger
	CollectorHostname func(context.Context, string) (string, error)
	Collectors        CollectorSource
	Queries           QueryExecutor
	Replay            bool
	Observer          TransitionObserver
}

type key struct{ rule, collector, target, instance string }
type targetKey struct{ collector, target string }
type targetView struct{ state, reason, process, port, service string }
type probeView struct {
	ok      bool
	message string
	kind    string
	metrics map[string]float64
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
	closing   bool
}

type pendingTransition struct {
	alert Alert
	event Event
}

type Engine struct {
	observer          TransitionObserver
	mu                sync.Mutex
	repository        Repository
	notifications     NotificationSink
	now               func() time.Time
	clock             collector.ProbeClock
	newID             func() string
	specs             []ruleSpec
	states            map[key]*ruleState
	collectors        map[string]bool
	targets           map[targetKey]targetView
	probes            map[targetKey]map[int]probeView
	logger            *slog.Logger
	collectorHostname func(context.Context, string) (string, error)
	hostnames         map[string]string
	assignments       map[string]config.Target
	collectorSource   CollectorSource
	presence          map[string]CollectorPresence
	presenceKnown     bool
	systems           map[string]collector.SystemMetrics
	processes         map[targetKey]map[int32]pipeline.ProcessRef
	queries           QueryExecutor
	sql               map[string]*sqlSnapshot
	backfill          map[string]bool
	dataWindows       map[key]dataWindow
	resumeSince       map[key]time.Time
	replay            bool
}

func New(ctx context.Context, options Options) (*Engine, error) {
	if options.Repository == nil || options.Notifications == nil {
		return nil, fmt.Errorf("规则引擎需要告警存储与通知队列")
	}
	specs, err := compile(options.Config, options.Collectors != nil)
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
	e.observer = options.Observer
	e.collectorHostname, e.hostnames = options.CollectorHostname, make(map[string]string)
	e.collectorSource = options.Collectors
	e.systems, e.processes, e.sql, e.queries = make(map[string]collector.SystemMetrics), make(map[targetKey]map[int32]pipeline.ProcessRef), make(map[string]*sqlSnapshot), options.Queries
	e.backfill, e.dataWindows, e.resumeSince, e.replay = make(map[string]bool), make(map[key]dataWindow), make(map[key]time.Time), options.Replay
	e.updateAssignments(options.Config)
	active, err := options.Repository.ActiveAlerts(ctx)
	if err != nil {
		return nil, fmt.Errorf("恢复触发中告警失败：%w", err)
	}
	for _, alert := range active {
		identity := key{alert.RuleID, alert.CollectorID, alert.TargetID, alert.InstanceKey}
		if alert.ID == "" || alert.RuleID == "" || alert.CollectorID == "" || alert.TargetID == "" || alert.State != StateFiring || e.states[identity] != nil {
			return nil, fmt.Errorf("存储中存在无效或重复的触发中告警，请检查告警数据")
		}
		e.states[identity] = &ruleState{phase: StateFiring, alert: alert}
		e.collectors[alert.CollectorID] = true
		if err := e.loadHostname(ctx, alert.CollectorID); err != nil {
			return nil, err
		}
	}
	e.markRetired(e.specs)
	if err := e.retryClosures(ctx); err != nil {
		e.logger.Warn("Retired alert closure failed; retrying during evaluation")
	}
	return e, nil
}

func compile(cfg *config.Config, includeDefaultOffline bool) ([]ruleSpec, error) {
	if cfg == nil {
		return nil, fmt.Errorf("规则配置不能为空")
	}
	targets := make(map[string]config.Target, len(cfg.Targets))
	for _, target := range cfg.Targets {
		targets[target.ID] = target
	}
	seen := make(map[string]bool)
	definitions := slices.Clone(cfg.Rules)
	hasOffline := false
	for i, rule := range definitions {
		if rule.Type == "collector_offline" {
			hasOffline = true
			if rule.For == "" {
				definitions[i].For = "30s"
			}
		}
	}
	if includeDefaultOffline && !hasOffline {
		definitions = append(definitions, config.OfflineRules(cfg)...)
	}
	specs := make([]ruleSpec, 0, len(definitions))
	for _, rule := range definitions {
		if rule.Type == "sql" && rule.Interval == "" {
			rule.Interval = "1m"
		}
		target, exists := targets[rule.Target]
		if rule.Type == "collector_offline" && rule.Target == "" {
			target, exists = config.Target{ID: CollectorTargetID, Name: "采集器连接"}, true
		}
		if rule.Target == "" && !rule.NeedsTarget() && rule.Type != "collector_offline" {
			target, exists = config.Target{ID: SystemTargetID, Name: "系统"}, true
			if rule.Type == "sql" {
				target.ID, target.Name = SQLTargetID, "SQL 查询"
			}
		}
		delay, err := rule.For.Parse()
		if rule.ID == "" || seen[rule.ID] || !exists || err != nil || delay < 0 {
			return nil, fmt.Errorf("规则 ID、目标引用或持续时间无效，请先运行 config validate")
		}
		switch rule.Type {
		case "collector_offline":
			if rule.Target != "" {
				return nil, fmt.Errorf("collector_offline 自动适用全部采集器，不填写 target")
			}
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
		case "metric_threshold", "service_stopped", "script_failed", "sql":
		default:
			return nil, fmt.Errorf("规则类型无效，请先运行 config validate")
		}
		if err := rule.ValidateCondition(target); err != nil {
			return nil, fmt.Errorf("规则 %s：%w", rule.ID, err)
		}
		if rule.Threshold != nil {
			value := *rule.Threshold
			rule.Threshold = &value
		}
		if rule.Probe != nil {
			value := *rule.Probe
			rule.Probe = &value
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
	return e.UpdateConfigContext(context.Background(), cfg)
}

func (e *Engine) UpdateConfigContext(ctx context.Context, cfg *config.Config) error {
	specs, err := compile(cfg, e.collectorSource != nil)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.markRetired(specs)
	e.updateAssignments(cfg)
	for _, next := range specs {
		for _, previous := range e.specs {
			if previous.rule.ID != next.rule.ID {
				continue
			}
			if previous.rule.Type != next.rule.Type || previous.rule.Target != next.rule.Target || previous.delay != next.delay {
				for id, state := range e.states {
					if id.rule == next.rule.ID && state.phase != StateFiring && state.next == nil && !state.closing {
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
	e.specs = specs
	e.dataWindows, e.resumeSince = make(map[key]dataWindow), make(map[key]time.Time)
	for id := range e.sql {
		delete(e.sql, id)
	}
	return e.retryClosures(ctx)
}

const closedMessage = "规则已删除或修改，告警已关闭"

func (e *Engine) markRetired(next []ruleSpec) {
	configured := make(map[string]ruleSpec, len(next))
	previous := make(map[string]ruleSpec, len(e.specs))
	for _, spec := range next {
		configured[spec.rule.ID] = spec
	}
	for _, spec := range e.specs {
		previous[spec.rule.ID] = spec
	}
	active := func(id key) bool {
		spec, exists := configured[id.rule]
		old, hadOld := previous[id.rule]
		paired := spec.rule.Type != "collector_offline" || !e.presenceKnown || e.presence[id.collector].ID != ""
		return exists && paired && spec.target.ID == id.target && spec.target.AppliesTo(id.collector, e.hostnames[id.collector]) && (!hadOld || sameCondition(old.rule, spec.rule))
	}
	for id := range e.dataWindows {
		if !active(id) {
			delete(e.dataWindows, id)
		}
	}
	for id := range e.resumeSince {
		if !active(id) {
			delete(e.resumeSince, id)
		}
	}
	at := e.now().UTC().UnixMilli()
	for id, state := range e.states {
		if state.closing {
			continue
		}
		if active(id) {
			continue
		}
		if state.phase != StateFiring && state.next == nil {
			delete(e.states, id)
			continue
		}
		alert := state.alert
		if state.next != nil {
			alert = state.next.alert
		}
		alert.State, alert.ResolvedAt, alert.Summary = StateResolved, at, closedMessage
		state.next = &pendingTransition{alert: alert, event: Event{AlertID: alert.ID, TS: at, Kind: "closed", Message: closedMessage}}
		state.notify, state.closing = false, true
	}
}

func (e *Engine) retryClosures(ctx context.Context) error {
	var failures []error
	for id, state := range e.states {
		if !state.closing {
			continue
		}
		if err := e.finishTransition(ctx, state); err != nil {
			failures = append(failures, err)
		} else {
			delete(e.states, id)
		}
	}
	return errors.Join(failures...)
}

func (e *Engine) Handle(ctx context.Context, message pipeline.Message) error {
	return e.HandleBatch(ctx, []pipeline.Message{message})
}

func (e *Engine) HandleBatch(ctx context.Context, messages []pipeline.Message) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("处理采集记录已取消：%w", err)
	}
	for _, message := range messages {
		if err := message.Validate(); err != nil {
			return err
		}
	}
	e.mu.Lock()
	for _, message := range messages {
		if err := e.observe(ctx, message); err != nil {
			e.mu.Unlock()
			return err
		}
		e.observeBackfill(message)
	}
	failure := e.evaluate(ctx)
	jobs := e.prepareSQL(e.now())
	e.mu.Unlock()
	return errors.Join(failure, e.runSQL(ctx, jobs))
}

func (e *Engine) observe(ctx context.Context, message pipeline.Message) error {
	if err := e.loadHostname(ctx, message.CollectorID); err != nil {
		return err
	}
	e.collectors[message.CollectorID] = true
	var targetID string
	if message.TargetStatus != nil {
		targetID = message.TargetStatus.TargetID
	} else if message.ProbeResult != nil {
		targetID = message.ProbeResult.TargetID
	}
	if targetID != "" {
		assignment, exists := e.assignments[targetID]
		if !exists || !assignment.AppliesTo(message.CollectorID, e.hostnames[message.CollectorID]) {
			return nil
		}
	}
	switch message.Kind {
	case pipeline.KindTargetStatus:
		status := message.TargetStatus
		id := targetKey{message.CollectorID, status.TargetID}
		e.targets[id] = targetView{status.State, status.Reason, status.Process, status.Port, status.Service}
		e.processes[id] = make(map[int32]pipeline.ProcessRef, len(status.Processes))
		for _, process := range status.Processes {
			e.processes[id][process.PID] = process
		}
	case pipeline.KindProbeResult:
		probe := message.ProbeResult
		id := targetKey{message.CollectorID, probe.TargetID}
		if e.probes[id] == nil {
			e.probes[id] = make(map[int]probeView)
		}
		e.probes[id][probe.Probe] = probeView{probe.OK, probe.Error, probe.Type, cloneMetrics(probe.Metrics)}
	case pipeline.KindSystemMetrics:
		e.systems[message.CollectorID] = cloneSystem(*message.SystemMetrics)
	case pipeline.KindProcessSample:
		sample := message.ProcessSample
		id := targetKey{message.CollectorID, sample.TargetID}
		if old, exists := e.processes[id][sample.PID]; exists && old.CreatedAt == sample.CreatedAt {
			old.CPUPercent, old.RSS = sample.CPUPercent, sample.RSS
			e.processes[id][sample.PID] = old
		}
	default:
		return nil
	}
	return nil
}

func (e *Engine) Evaluate(ctx context.Context) error {
	e.mu.Lock()
	failure := e.evaluate(ctx)
	jobs := e.prepareSQL(e.now())
	e.mu.Unlock()
	return errors.Join(failure, e.runSQL(ctx, jobs))
}

func (e *Engine) evaluate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("规则评估已取消：%w", err)
	}
	now := e.now()
	presenceErr := e.refreshPresence(ctx)
	collectors := make([]string, 0, len(e.collectors))
	for id := range e.collectors {
		collectors = append(collectors, id)
	}
	sort.Strings(collectors)
	var failures []error
	if presenceErr != nil {
		failures = append(failures, presenceErr)
	}
	if err := e.retryClosures(ctx); err != nil {
		failures = append(failures, err)
	}
	for _, spec := range e.specs {
		if spec.rule.Type == "sql" {
			if err := e.retrySQL(ctx, spec, now); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		if spec.rule.Type == "collector_offline" && (e.collectorSource == nil || presenceErr != nil) {
			continue
		}
		for _, id := range collectors {
			if spec.rule.Type != "collector_offline" && e.backfill[id] {
				continue
			}
			if spec.rule.Type == "collector_offline" && e.presence[id].ID == "" {
				continue
			}
			if !spec.target.AppliesTo(id, e.hostnames[id]) {
				continue
			}
			if err := e.evaluateRule(ctx, spec, id, now); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

func (e *Engine) loadHostname(ctx context.Context, id string) error {
	if e.collectorHostname == nil {
		return nil
	}
	hostname, err := e.collectorHostname(ctx, id)
	if err != nil {
		return fmt.Errorf("读取规则适用的采集器主机名失败：%w", err)
	}
	previous, existed := e.hostnames[id]
	e.hostnames[id] = hostname
	if existed && previous != hostname {
		e.markRetired(e.specs)
	}
	return nil
}

func (e *Engine) updateAssignments(cfg *config.Config) {
	next := make(map[string]config.Target, len(cfg.Targets))
	for _, target := range cfg.Targets {
		next[target.ID] = config.Target{ID: target.ID, Collector: target.Collector}
	}
	for targetID, previous := range e.assignments {
		updated, exists := next[targetID]
		if exists && previous.Collector == updated.Collector {
			continue
		}
		for id := range e.targets {
			if id.target == targetID {
				delete(e.targets, id)
			}
		}
		for id := range e.probes {
			if id.target == targetID {
				delete(e.probes, id)
			}
		}
	}
	e.assignments = next
}

func (e *Engine) evaluateRule(ctx context.Context, spec ruleSpec, id string, now time.Time) error {
	met, known, reason := e.condition(spec, id)
	return e.evaluateCondition(ctx, spec, key{spec.rule.ID, id, spec.target.ID, ""}, now, met, known, reason)
}

func (e *Engine) evaluateCondition(ctx context.Context, spec ruleSpec, identity key, now time.Time, met, known bool, reason string) error {
	id := identity.collector
	state := e.states[identity]
	if state == nil {
		state = &ruleState{phase: StateOK}
		e.states[identity] = state
	}
	if state.closing {
		return nil
	}
	// An uncertain commit is retried before reacting to newer data, using the original ID and event.
	if err := e.finishTransition(ctx, state); err != nil {
		return err
	}
	if !known || !met {
		delete(e.resumeSince, identity)
		if state.phase == StatePending {
			state.phase, state.pendingAt = StateOK, time.Time{}
		}
		if known && !met && state.phase == StateFiring {
			alert := state.alert
			alert.State, alert.ResolvedAt = StateResolved, now.UTC().UnixMilli()
			alert.Summary = fmt.Sprintf("%s：%s 已恢复", display(spec.rule.Name, spec.rule.ID), display(spec.target.Name, spec.target.ID))
			if spec.rule.Type == "collector_offline" {
				alert.Summary = fmt.Sprintf("%s：采集器 %s 已恢复连接", display(spec.rule.Name, spec.rule.ID), display(e.hostnames[id], id))
			}
			return e.transition(ctx, state, alert, now)
		}
		return nil
	}
	if state.phase == StateFiring {
		delete(e.resumeSince, identity)
		return e.renotify(ctx, spec, state, now)
	}
	if state.phase != StatePending {
		state.phase, state.pendingAt, state.alert = StatePending, now, Alert{}
	}
	if since, exists := e.resumeSince[identity]; exists {
		state.pendingAt = since
		delete(e.resumeSince, identity)
	}
	if spec.rule.Type == "collector_offline" {
		state.pendingAt = time.UnixMilli(e.presence[id].LastSeen)
	}
	if now.Sub(state.pendingAt) < spec.delay {
		return nil
	}
	alert := Alert{ID: e.newID(), RuleID: spec.rule.ID, CollectorID: id, TargetID: spec.target.ID, InstanceKey: identity.instance, State: StateFiring, Severity: spec.rule.Severity, StartedAt: state.pendingAt.UTC().UnixMilli(), Summary: fmt.Sprintf("%s：%s %s", display(spec.rule.Name, spec.rule.ID), display(spec.target.Name, spec.target.ID), reason)}
	if spec.rule.Type == "collector_offline" {
		alert.Summary = fmt.Sprintf("%s：采集器 %s 已失联，请检查采集器进程与网络", display(spec.rule.Name, spec.rule.ID), display(e.hostnames[id], id))
	}
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
	return e.notificationTransition(ctx, state, alert, alert.State, at)
}

func (e *Engine) notificationTransition(ctx context.Context, state *ruleState, alert Alert, kind string, at time.Time) error {
	alert.NotificationAt, alert.NotificationKey = at.UTC().UnixMilli(), fmt.Sprintf("%s:%d", kind, at.UTC().UnixMilli())
	event := Event{AlertID: alert.ID, TS: at.UTC().UnixMilli(), Kind: kind, Message: alert.Summary}
	state.next = &pendingTransition{alert: alert, event: event}
	return e.finishTransition(ctx, state)
}

func (e *Engine) finishTransition(ctx context.Context, state *ruleState) error {
	if state.next != nil {
		if err := e.repository.SaveTransition(ctx, state.next.alert, state.next.event); err != nil {
			return fmt.Errorf("保存告警状态失败，下次评估将重试：%w", err)
		}
		if e.observer != nil {
			e.observer.Transition(state.next.alert, state.next.event)
		}
		state.phase, state.alert = state.next.alert.State, state.next.alert
		state.notify = state.next.event.Kind != "closed"
		state.next = nil
	}
	if state.notify {
		alert := state.alert
		if policy, ok := e.repository.(NotificationPolicy); ok {
			decision, err := policy.EvaluateNotification(ctx, alert, e.now())
			if err != nil {
				return fmt.Errorf("读取告警通知策略失败，下次评估将重试：%w", err)
			}
			if decision.Suppressed != "" {
				state.notify = false
				return nil
			}
			alert.Flapping = decision.Digest
		}
		if err := e.notifications.Enqueue(ctx, alert); err != nil {
			return fmt.Errorf("加入告警通知队列失败，下次评估将重试：%w", err)
		}
		state.notify = false
	}
	return nil
}

func (e *Engine) condition(spec ruleSpec, collectorID string) (met, known bool, reason string) {
	if spec.rule.Type == "collector_offline" {
		return e.collectorCondition(spec, collectorID)
	}
	if spec.rule.Type == "metric_threshold" {
		return e.metricCondition(spec, collectorID)
	}
	id := targetKey{collectorID, spec.target.ID}
	target, exists := e.targets[id]
	if !exists {
		return false, false, ""
	}
	switch spec.rule.Type {
	case "service_stopped":
		switch target.service {
		case "stopped", "missing", "deleted", "paused":
			return true, true, "服务已停止"
		case "running":
			return false, true, ""
		default:
			return false, false, ""
		}
	case "script_failed":
		return e.scriptCondition(spec, id)
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
	InstanceKey string `json:"instanceKey,omitempty"`
}

func (e *Engine) Snapshot() []Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]Status, 0, len(e.states))
	for id, state := range e.states {
		status := Status{RuleID: id.rule, CollectorID: id.collector, TargetID: id.target, InstanceKey: id.instance, State: state.phase, AlertID: state.alert.ID}
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
		if a.TargetID != b.TargetID {
			return a.TargetID < b.TargetID
		}
		return a.InstanceKey < b.InstanceKey
	})
	return result
}
