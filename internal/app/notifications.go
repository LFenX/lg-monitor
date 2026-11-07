package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

type SecretReader interface {
	Get(context.Context, string) ([]byte, error)
}

type ClientFactory func(config.FeishuApp, []byte) (feishu.Client, error)

type notificationJob struct {
	input     cards.Input
	rule      config.Rule
	notifiers []config.Notifier
	apps      []config.FeishuApp
	finish    func()
}

type notificationQueue struct {
	observer           RuntimeObserver
	active             *config.Config
	secrets            SecretReader
	factory            ClientFactory
	webhookFactory     WebhookFactory
	jsonWebhookFactory JSONWebhookFactory
	allow              func(context.Context) (bool, error)
	store              Persistence
	renderer           *cards.Renderer
	host               cards.Host
	now                func() time.Time
	logger             *slog.Logger
	waiter             notify.Waiter
	toast              notify.ToastClient
	jobs               chan notificationJob
	mu                 sync.Mutex
	targets            map[targetKey]pipeline.TargetStatus
	probes             map[targetKey]map[int]pipeline.ProbeResult
	clients            map[string]cachedClient
	pauseFlights       map[string]bool
	lastPause          *store.NotificationPause
}

type cachedClient struct {
	fingerprint [32]byte
	client      feishu.Client
}

type targetKey struct{ collector, target string }

func (q *notificationQueue) setConfig(cfg *config.Config) {
	q.mu.Lock()
	defer q.mu.Unlock()
	keys := make(map[targetKey]bool, len(q.probes)+len(q.targets))
	for key := range q.probes {
		keys[key] = true
	}
	for key := range q.targets {
		keys[key] = true
	}
	for key := range keys {
		var previous, next *config.Target
		for i := range q.active.Targets {
			if q.active.Targets[i].ID == key.target {
				previous = &q.active.Targets[i]
			}
		}
		for i := range cfg.Targets {
			if cfg.Targets[i].ID == key.target {
				next = &cfg.Targets[i]
			}
		}
		if previous == nil || next == nil || !reflect.DeepEqual(previous.Match, next.Match) || !reflect.DeepEqual(previous.Probes, next.Probes) {
			delete(q.probes, key)
			delete(q.targets, key)
		}
	}
	q.active = cfg
}

func (q *notificationQueue) observe(message pipeline.Message) {
	if q.observer != nil {
		q.observer.Sample(message)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	switch message.Kind {
	case pipeline.KindTargetStatus:
		q.targets[targetKey{message.CollectorID, message.TargetStatus.TargetID}] = *message.TargetStatus
	case pipeline.KindProbeResult:
		key := targetKey{message.CollectorID, message.ProbeResult.TargetID}
		if q.probes[key] == nil {
			q.probes[key] = make(map[int]pipeline.ProbeResult)
		}
		q.probes[key][message.ProbeResult.Probe] = *message.ProbeResult
	case pipeline.KindProcessSample:
		sample := message.ProcessSample
		key := targetKey{message.CollectorID, sample.TargetID}
		status := q.targets[key]
		for i := range status.Processes {
			if status.Processes[i].PID == sample.PID && status.Processes[i].CreatedAt == sample.CreatedAt {
				status.Processes[i].CPUPercent, status.Processes[i].RSS = sample.CPUPercent, sample.RSS
			}
		}
		q.targets[key] = status
	}
}

func (q *notificationQueue) job(alert rules.Alert) (notificationJob, error) {
	q.mu.Lock()
	cfg := q.active
	q.mu.Unlock()
	job := notificationJob{input: cards.Input{Alert: alert, Host: q.host, Detail: alert.Summary}}
	job.input.Target = config.Target{ID: alert.TargetID, Name: alert.TargetID}
	job.input.Host.CollectorID = alert.CollectorID
	if alert.CollectorID != q.host.CollectorID {
		job.input.Host.Name = alert.CollectorID
	}
	for _, rule := range cfg.Rules {
		if rule.ID == alert.RuleID {
			job.rule, job.input.Rule = rule, rule
			break
		}
	}
	if alert.TargetID == rules.CollectorTargetID {
		for _, rule := range config.OfflineRules(cfg) {
			if rule.ID == alert.RuleID {
				job.rule, job.input.Rule = rule, rule
				job.input.Target = config.Target{ID: rules.CollectorTargetID, Name: "采集器连接"}
				break
			}
		}
	}
	for _, target := range cfg.Targets {
		if target.ID == alert.TargetID {
			job.input.Target = target
			break
		}
	}
	for _, id := range job.rule.Notify {
		for _, notifier := range cfg.Notifiers {
			if notifier.ID == id {
				job.notifiers = append(job.notifiers, notifier)
			}
		}
	}
	job.apps = cfg.FeishuApps
	q.mu.Lock()
	key := targetKey{alert.CollectorID, alert.TargetID}
	if status, ok := q.targets[key]; ok {
		job.input.Detail = status.Reason
		for _, ref := range status.Processes {
			job.input.Processes = append(job.input.Processes, collector.Process{PID: ref.PID, ParentPID: ref.ParentPID, Name: ref.Name, Exe: ref.Exe, CreatedAt: ref.CreatedAt, CPUPercent: ref.CPUPercent, RSS: ref.RSS, Threads: ref.Threads})
		}
	}
	if probes := q.probes[key]; len(probes) > 0 && job.rule.Type == "probe_failed" {
		indices := make([]int, 0, len(probes))
		for index := range probes {
			indices = append(indices, index)
		}
		slices.Sort(indices)
		probe := probes[indices[len(indices)-1]]
		for _, index := range indices {
			if !probes[index].OK && alert.State == rules.StateFiring {
				probe = probes[index]
				break
			}
		}
		if probe.StatusCode != 0 {
			job.input.Value = fmt.Sprintf("HTTP %d · %dms", probe.StatusCode, probe.LatencyMS)
		} else {
			job.input.Value = probe.Error
		}
	}
	q.mu.Unlock()
	if strings.HasPrefix(alert.NotificationKey, "resume:") {
		job.input.Detail = "通知暂停期间发生，恢复通知后补发；" + job.input.Detail
	}
	if len(job.notifiers) == 0 {
		return notificationJob{}, fmt.Errorf("告警规则 %s 没有可用的通知渠道", alert.RuleID)
	}
	return job, nil
}

var ErrNotificationQueueFull = errors.New("通知队列已满，将在下次评估重试")

func (q *notificationQueue) Enqueue(ctx context.Context, alert rules.Alert) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("告警通知入队已取消：%w", err)
	}
	job, err := q.job(alert)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("告警通知入队已取消：%w", ctx.Err())
	case q.jobs <- job:
		return nil
	default:
		q.logger.Error("Notification queue full; rule engine will retry", "alert", alert.ID)
		return ErrNotificationQueueFull
	}
}

func (q *notificationQueue) replay(ctx context.Context) error {
	now := q.now()
	var cursor int64
	for {
		pending, err := q.store.PendingNotifications(ctx, now.Add(-24*time.Hour).UnixMilli(), now.UnixMilli(), cursor, 256)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		for _, item := range pending {
			cursor = item.EventID
			job, err := q.job(item.Alert)
			if err != nil {
				q.logger.Warn("Pending notification has no configured destination", "alert", item.Alert.ID)
				continue
			}
			job.notifiers = slices.DeleteFunc(job.notifiers, func(n config.Notifier) bool {
				return slices.Contains(item.Successful, n.ID) || slices.Contains(item.Skipped, n.ID)
			})
			if len(job.notifiers) == 0 {
				continue
			}
			// Startup recovery precedes live evaluation; cancellation still interrupts a full queue.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case q.jobs <- job:
			}
		}
	}
}

func (q *notificationQueue) getClient(ctx context.Context, app config.FeishuApp) (feishu.Client, error) {
	secret, err := q.secrets.Get(ctx, app.SecretRef)
	defer clear(secret)
	if err != nil {
		return nil, &runtimeError{message: "读取飞书应用密钥失败，请检查密钥库", cause: err}
	}
	encoded, err := json.Marshal(app)
	if err != nil {
		return nil, fmt.Errorf("编码飞书应用配置失败：%w", err)
	}
	material := append(encoded, secret...)
	fingerprint := sha256.Sum256(material)
	clear(material)
	if cached, ok := q.clients[app.ID]; ok {
		if cached.fingerprint == fingerprint {
			return cached.client, nil
		}
		_ = cached.client.Close()
		delete(q.clients, app.ID)
	}
	client, err := q.factory(app, secret)
	if err != nil {
		return nil, &runtimeError{message: "创建飞书通知客户端失败", cause: err}
	}
	q.clients[app.ID] = cachedClient{fingerprint: fingerprint, client: client}
	return client, nil
}

func (q *notificationQueue) run(ctx context.Context) error {
	workers := map[string]*notificationDeliveryQueue{}
	var running sync.WaitGroup
	defer func() {
		for _, worker := range workers {
			worker.close()
		}
		running.Wait()
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case job, ok := <-q.jobs:
			if !ok {
				return nil
			}
			batch := &notificationDeliveryBatch{left: len(job.notifiers)}
			if batch.left == 0 && job.finish != nil {
				job.finish()
			}
			for _, notifier := range job.notifiers {
				worker := workers[notifier.ID]
				if worker == nil {
					worker = newNotificationDeliveryQueue()
					workers[notifier.ID] = worker
					running.Add(1)
					go func(worker *notificationDeliveryQueue) { defer running.Done(); q.runDelivery(ctx, worker) }(worker)
				}
				worker.push(notificationDelivery{job: job, notifier: notifier, batch: batch})
			}
		}
	}
}

func (q *notificationQueue) readDecision(ctx context.Context, policy rules.NotificationPolicy, alert rules.Alert) (rules.Decision, error) {
	var decision rules.Decision
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		decision, err = policy.EvaluateNotification(ctx, alert, q.now())
		if err == nil || ctx.Err() != nil {
			return decision, err
		}
		if attempt == 2 {
			break
		}
		if q.waiter != nil {
			if waitErr := q.waiter.Wait(ctx, time.Second); waitErr != nil {
				return decision, waitErr
			}
		} else {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return decision, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return decision, err
}

func (q *notificationQueue) send(ctx context.Context, job notificationJob, notifier config.Notifier) error {
	if notifier.Type == "webhook" {
		client, err := openJSONWebhook(ctx, q.secrets, q.jsonWebhookFactory, notifier)
		if err != nil {
			return q.recordPreparationFailure(ctx, job, notifier, err)
		}
		defer client.Close()
		body, err := json.Marshal(notify.WebhookPayload(job.input, q.now(), false))
		if err != nil {
			return err
		}
		sender, err := notify.NewWebhookSender(notify.WebhookSenderOptions{Client: client, Recorder: q.store, Now: q.now, Waiter: q.waiter, Logger: q.logger, Allow: q.allow})
		if err != nil {
			return err
		}
		return sender.Send(ctx, notify.WebhookRequest{AlertID: job.input.Alert.ID, State: job.input.Alert.State, EventKey: job.input.Alert.NotificationKey, NotifierID: notifier.ID, Body: body})
	}
	if notifier.Type == "toast" {
		sender, err := notify.NewToastSender(notify.ToastSenderOptions{Client: q.toast, Recorder: q.store, Now: q.now, Allow: q.allow})
		if err != nil {
			return err
		}
		_, err = sender.Send(ctx, notify.ToastRequest{NotifierID: notifier.ID, AlertID: job.input.Alert.ID, State: job.input.Alert.State, EventKey: job.input.Alert.NotificationKey, Value: toastContent(job.input)})
		return err
	}
	var client notify.CardSender
	var err error
	if notifier.Type == "feishu_webhook" {
		var hook feishu.WebhookClient
		hook, err = openWebhook(ctx, q.secrets, q.webhookFactory, notifier)
		if err == nil {
			defer hook.Close()
			client = hook
		}
	} else {
		var app config.FeishuApp
		for _, candidate := range job.apps {
			if candidate.ID == notifier.App {
				app = candidate
				break
			}
		}
		client, err = q.getClient(ctx, app)
	}
	if err != nil {
		id := "preflight-" + rand.Text()
		if auditErr := q.store.RecordNotification(ctx, notify.Attempt{ID: id, AlertID: job.input.Alert.ID, State: job.input.Alert.State, EventKey: job.input.Alert.NotificationKey, NotifierID: notifier.ID, Template: notifier.Template, TS: q.now().UnixMilli(), Error: "读取通知密钥或创建客户端失败，请检查渠道配置"}); auditErr != nil {
			return fmt.Errorf("记录通知准备失败：%w", auditErr)
		}
		return err
	}
	sender, err := notify.NewSender(notify.Options{Client: client, Recorder: q.store, Now: q.now, Logger: q.logger, Waiter: q.waiter, Allow: q.allow})
	if err != nil {
		return err
	}
	selected := job.rule.Template
	if selected == "" {
		selected = notifier.Template
	}
	card := q.renderer.Render(selected, job.input)
	request := notify.Request{Message: feishu.CardMessage{AlertID: job.input.Alert.ID, State: job.input.Alert.State, EventKey: job.input.Alert.NotificationKey, ChatID: notifier.ChatID, Card: card.JSON}, NotifierID: notifier.ID, Template: card.Template}
	_, err = sender.Send(ctx, request)
	var rejected *feishu.Error
	if err != nil && errors.As(err, &rejected) && rejected.Kind == feishu.CardRejected && card.Template != "standard" && card.Template != "compact" {
		fallback := q.renderer.StandardFallback(card.Template, job.input)
		request.Message.Card, request.Template = fallback.JSON, fallback.Template
		_, err = sender.Send(ctx, request)
	}
	return err
}

func (q *notificationQueue) recordPreparationFailure(ctx context.Context, job notificationJob, notifier config.Notifier, cause error) error {
	if err := q.store.RecordNotification(ctx, notify.Attempt{ID: "preflight-" + rand.Text(), AlertID: job.input.Alert.ID, State: job.input.Alert.State, EventKey: job.input.Alert.NotificationKey, NotifierID: notifier.ID, TS: q.now().UnixMilli(), Error: "读取通知凭据或创建客户端失败，请检查渠道配置"}); err != nil {
		return fmt.Errorf("记录通知准备失败：%w", err)
	}
	return cause
}
