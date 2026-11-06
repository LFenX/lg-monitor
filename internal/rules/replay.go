package rules

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

const MaxReplayRecords = 200000
const ReplayRetention = 24 * time.Hour
const ReplayBudget = 60 * time.Second

type ReplayData struct {
	Messages  []pipeline.Message
	Hostnames map[string]string
	Warnings  []string
}
type ReplaySource interface {
	LoadReplay(context.Context, config.Rule, int64, int64) (ReplayData, error)
}
type ReplayQueryConnection interface {
	QueryExecutor
	Close() error
}
type ReplayQueryOpener interface {
	OpenReplayQueries(context.Context) (ReplayQueryConnection, error)
}
type ReplayOptions struct {
	Config       *config.Config
	Rule         config.Rule
	Since, Until time.Time
	Now          func() time.Time
	Source       ReplaySource
}
type ReplayEvent struct {
	Key       string `json:"key"`
	State     string `json:"state"`
	TS        int64  `json:"ts"`
	StartedAt int64  `json:"startedAt,omitempty"`
	Summary   string `json:"summary,omitempty"`
}
type ReplayTotals struct {
	Fired       int `json:"fired"`
	Resolved    int `json:"resolved"`
	FiringAtEnd int `json:"firingAtEnd"`
}
type ReplayError struct {
	TS      int64  `json:"ts"`
	Message string `json:"message"`
}
type ReplayErrors struct {
	Count int          `json:"count"`
	First *ReplayError `json:"first"`
}
type ReplayResult struct {
	Rule          string        `json:"rule"`
	Type          string        `json:"type"`
	RequestedFrom int64         `json:"requestedFrom"`
	From          int64         `json:"from"`
	To            int64         `json:"to"`
	Step          int64         `json:"step"`
	Evaluations   int           `json:"evaluations"`
	Complete      bool          `json:"complete"`
	Events        []ReplayEvent `json:"events"`
	Totals        ReplayTotals  `json:"totals"`
	Errors        ReplayErrors  `json:"errors"`
	Notes         []string      `json:"notes"`
}
type replayRepository struct {
	events []ReplayEvent
	active map[string]Alert
	totals ReplayTotals
}

func (r *replayRepository) ActiveAlerts(context.Context) ([]Alert, error) { return nil, nil }
func (r *replayRepository) SaveTransition(_ context.Context, alert Alert, event Event) error {
	key := alert.InstanceKey
	if key == "" {
		key = alert.CollectorID + "/" + alert.TargetID
	}
	switch event.Kind {
	case StateFiring:
		r.active[alert.ID] = alert
		r.totals.Fired++
		r.events = append(r.events, ReplayEvent{Key: key, State: StateFiring, TS: event.TS, StartedAt: alert.StartedAt, Summary: alert.Summary})
	case StateResolved:
		delete(r.active, alert.ID)
		r.totals.Resolved++
		r.events = append(r.events, ReplayEvent{Key: key, State: StateResolved, TS: event.TS})
	case "closed":
		delete(r.active, alert.ID)
	}
	return nil
}

type replayNotifications struct{}

func (replayNotifications) Enqueue(context.Context, Alert) error { return nil }

func replayCount(from, to, step int64) (int, error) {
	if step <= 0 || to < from {
		return 0, fmt.Errorf("回放时间或评估间隔无效")
	}
	count := (to-from)/step + 1
	if count > 10000 {
		return 0, fmt.Errorf("回放需要 %d 次评估，超过 10000 次；按此间隔最多回放 %s", count, time.Duration(step*9999)*time.Millisecond)
	}
	return int(count), nil
}

func Replay(parent context.Context, opts ReplayOptions) (result ReplayResult, replayErr error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Until.IsZero() {
		opts.Until = opts.Now()
	}
	if opts.Since.IsZero() {
		opts.Since = opts.Until.Add(-ReplayRetention)
	}
	result = ReplayResult{Rule: opts.Rule.ID, Type: opts.Rule.Type, RequestedFrom: opts.Since.UnixMilli(), From: opts.Since.UnixMilli(), To: opts.Until.UnixMilli(), Events: []ReplayEvent{}, Notes: []string{}}
	if opts.Config == nil || opts.Source == nil || opts.Rule.ID == "" || result.To <= result.From {
		return result, fmt.Errorf("回放需要已校验的规则定义、配置、历史源和正数时间范围")
	}
	if opts.Rule.Type == "collector_offline" {
		return result, fmt.Errorf("collector_offline 无法回放：数据库没有历史心跳，当前 last_seen 不能还原过去；请查询实际告警历史")
	}
	if minimum := result.To - ReplayRetention.Milliseconds(); result.From < minimum {
		result.From = minimum
		result.Notes = append(result.Notes, "请求起点超过原始数据保留时长，已截断到最近 24 小时")
	}
	opts.Since, opts.Until = time.UnixMilli(result.From), time.UnixMilli(result.To)
	result.Notes = append(result.Notes, "回放开始时没有告警，起点附近首次触发可能比实际晚；窗口统计查询在起点附近的数据可能不完整。", "按数据自带时间戳使用目前保留的数据和当前配置；离线补传的记录在实时评估时尚未到达，回放可能出现实际没有发生过的告警，实际记录以 alerts 为准。", "不模拟静音、确认、重复提醒或抖动合并，不发送通知，也不写入实际告警及通知表。")
	ctx, cancel := context.WithTimeout(parent, ReplayBudget)
	defer cancel()
	data := ReplayData{}
	var queries ReplayQueryConnection
	var err error
	if opts.Rule.Type == "sql" {
		interval, intervalErr := opts.Rule.SQLInterval()
		if intervalErr != nil {
			return result, intervalErr
		}
		result.Step = interval.Milliseconds()
		if _, err := replayCount(result.From, result.To, result.Step); err != nil {
			return result, err
		}
		opener, ok := opts.Source.(ReplayQueryOpener)
		if !ok {
			return result, fmt.Errorf("历史源不能打开独立的规则只读连接")
		}
		queries, err = opener.OpenReplayQueries(ctx)
		if err == nil {
			defer func() { replayErr = errors.Join(replayErr, queries.Close()) }()
		}
	} else {
		result.Step = 1000
		data, err = opts.Source.LoadReplay(ctx, opts.Rule, result.From, result.To)
		data.Messages = slices.Clone(data.Messages)
		result.Notes = append(result.Notes, data.Warnings...)
	}
	partial := func() ReplayResult {
		result.Complete = false
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Notes = append(result.Notes, "回放达到 60 秒总时限，返回已完成部分")
		} else {
			result.Notes = append(result.Notes, "回放已取消，返回已完成部分")
		}
		return result
	}
	if err != nil {
		if ctx.Err() != nil {
			return partial(), nil
		}
		return result, fmt.Errorf("准备规则回放失败：%w", err)
	}
	if len(data.Messages) > MaxReplayRecords {
		return result, fmt.Errorf("历史采样超过 %d 条，请缩小时间范围", MaxReplayRecords)
	}
	if opts.Rule.Type != "sql" && len(data.Messages) == 0 {
		result.Complete = true
		result.Notes = append(result.Notes, "没有可用历史采样，未推断为健康")
		return result, nil
	}
	for _, message := range data.Messages {
		if message.TS > result.To {
			return result, fmt.Errorf("历史源返回了未来采样，已拒绝回放")
		}
	}
	order := func(kind pipeline.Kind) int {
		switch kind {
		case pipeline.KindTargetStatus:
			return 0
		case pipeline.KindProcessSample:
			return 1
		case pipeline.KindProbeResult:
			return 2
		default:
			return 3
		}
	}
	slices.SortStableFunc(data.Messages, func(a, b pipeline.Message) int {
		at, bt := max(a.TS, result.From), max(b.TS, result.From)
		if at < bt {
			return -1
		}
		if at > bt {
			return 1
		}
		if a.TS < b.TS {
			return -1
		}
		if a.TS > b.TS {
			return 1
		}
		if order(a.Kind) != order(b.Kind) {
			return order(a.Kind) - order(b.Kind)
		}
		return strings.Compare(a.CollectorID, b.CollectorID)
	})
	cfg := *opts.Config
	rule := opts.Rule
	rule.Renotify = ""
	cfg.Rules = []config.Rule{rule}
	now := opts.Since
	repository := &replayRepository{events: []ReplayEvent{}, active: map[string]Alert{}}
	var sequence int
	engine, err := New(ctx, Options{Config: &cfg, Repository: repository, Notifications: replayNotifications{}, Now: func() time.Time { return now }, Queries: queries, NewID: func() string { sequence++; return fmt.Sprintf("replay-%d", sequence) }, CollectorHostname: func(_ context.Context, id string) (string, error) { return data.Hostnames[id], nil }})
	if err != nil {
		return result, err
	}
	finish := func() {
		result.Events = repository.events
		result.Totals = repository.totals
		result.Totals.FiringAtEnd = len(repository.active)
	}
	defer finish()
	index := 0
	for {
		if ctx.Err() != nil {
			return partial(), nil
		}
		var evaluationErr error
		if rule.Type == "sql" {
			evaluationErr = engine.Evaluate(ctx)
		} else {
			end := index
			for end < len(data.Messages) && max(data.Messages[end].TS, result.From) <= now.UnixMilli() {
				data.Messages[end].Seq = uint64(end + 1)
				end++
			}
			if end > index {
				evaluationErr = engine.HandleBatch(ctx, data.Messages[index:end])
				index = end
			} else {
				evaluationErr = engine.Evaluate(ctx)
			}
		}
		result.Evaluations++
		if evaluationErr != nil {
			if ctx.Err() != nil {
				return partial(), nil
			}
			if result.Evaluations == 1 {
				return result, evaluationErr
			}
			result.Errors.Count++
			if result.Errors.First == nil {
				result.Errors.First = &ReplayError{TS: now.UnixMilli(), Message: evaluationErr.Error()}
			}
		}
		if rule.Type == "sql" {
			next := now.UnixMilli() + result.Step
			if next > result.To {
				break
			}
			now = time.UnixMilli(next)
		} else {
			if now.UnixMilli() >= result.To {
				break
			}
			next := min(now.Add(time.Second).UnixMilli(), result.To)
			if index < len(data.Messages) {
				next = min(next, max(data.Messages[index].TS, result.From))
			}
			if next <= now.UnixMilli() {
				return result, fmt.Errorf("回放时间序列无效")
			}
			now = time.UnixMilli(next)
		}
	}
	result.Complete = true
	return result, nil
}
