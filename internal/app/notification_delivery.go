// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"slices"
	"sync"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
)

type notificationDelivery struct {
	job      notificationJob
	notifier config.Notifier
	batch    *notificationDeliveryBatch
	update   *eventLogCardUpdate
}
type notificationDeliveryBatch struct {
	mu      sync.Mutex
	left    int
	matched bool
}

// notificationDeliveryQueue is one channel's cards. Cards of the same alert go out one at a time in the order
// they came (a trigger before its recovery), but different alerts no longer wait behind each other: one card
// stuck in retries used to hold back every later trigger and recovery, which then arrived together.
type notificationDeliveryQueue struct {
	mu       sync.Mutex
	pending  []notificationDelivery
	inflight map[string]bool
	ready    chan struct{}
	closed   bool
	// The lanes of one channel share its clients, so the channel still holds one connection per app.
	clientsMu sync.Mutex
	clients   map[string]cachedClient
	lanes     int
}

// notificationLanes is how many alerts one channel sends at the same time.
const notificationLanes = 4

func newNotificationDeliveryQueue() *notificationDeliveryQueue {
	return &notificationDeliveryQueue{ready: make(chan struct{}, notificationLanes), inflight: map[string]bool{}, clients: map[string]cachedClient{}}
}

func (item notificationDelivery) alertID() string {
	if item.update != nil {
		return item.update.job.input.Alert.ID
	}
	return item.job.input.Alert.ID
}

// done frees the alert's lane, so its next card, if any, can go.
func (q *notificationDeliveryQueue) done(item notificationDelivery) {
	q.mu.Lock()
	delete(q.inflight, item.alertID())
	q.mu.Unlock()
	q.wake()
}

func (q *notificationDeliveryQueue) wake() {
	for range notificationLanes {
		select {
		case q.ready <- struct{}{}:
		default:
			return
		}
	}
}
func (q *notificationDeliveryQueue) push(item notificationDelivery) {
	q.mu.Lock()
	q.pending = append(q.pending, item)
	q.mu.Unlock()
	q.wake()
}
func (q *notificationDeliveryQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.wake()
}
func (q *notificationDeliveryQueue) next(ctx context.Context) (notificationDelivery, bool) {
	for {
		if ctx.Err() != nil {
			return notificationDelivery{}, false
		}
		q.mu.Lock()
		// The first card whose alert has nothing in flight; a later card of a busy alert waits its turn.
		blocked := map[string]bool{}
		for index, item := range q.pending {
			id := item.alertID()
			if q.inflight[id] || blocked[id] {
				blocked[id] = true
				continue
			}
			q.pending = append(q.pending[:index:index], q.pending[index+1:]...)
			if len(q.pending) == 0 {
				q.pending = nil
			}
			q.inflight[id] = true
			q.mu.Unlock()
			return item, true
		}
		closed := q.closed && len(q.pending) == 0
		q.mu.Unlock()
		if closed {
			return notificationDelivery{}, false
		}
		select {
		case <-ctx.Done():
			return notificationDelivery{}, false
		case <-q.ready:
		}
	}
}

func (q *notificationQueue) prepareDelivery(ctx context.Context, job notificationJob, destination config.Notifier) (notificationJob, bool) {
	q.mu.Lock()
	cfg := q.active
	q.mu.Unlock()
	var channel *config.Notifier
	for i := range cfg.Notifiers {
		if cfg.Notifiers[i].ID == destination.ID {
			channel = &cfg.Notifiers[i]
			break
		}
	}
	if channel == nil || channel.Type != destination.Type || channel.App != destination.App || channel.ChatID != destination.ChatID || channel.OpenID != destination.OpenID {
		return job, false
	}
	eligible := false
	for _, rule := range cfg.Rules {
		if rule.ID == job.input.Alert.RuleID && slices.Contains(rule.Notify, channel.ID) {
			eligible = true
			job.input.Rule = rule
		}
	}
	if job.input.Alert.TargetID == rules.CollectorTargetID {
		for _, rule := range config.OfflineRules(cfg) {
			if rule.ID == job.input.Alert.RuleID && slices.Contains(rule.Notify, channel.ID) {
				eligible = true
				job.input.Rule = rule
			}
		}
	}
	if !eligible {
		return job, false
	}
	job.input.Target = config.Target{ID: job.input.Alert.TargetID}
	if job.input.Alert.TargetID == rules.EventLogTargetID {
		job.input.Target.Name = "事件日志"
	}
	for _, target := range cfg.Targets {
		if target.ID == job.input.Alert.TargetID {
			job.input.Target = target
			break
		}
	}
	hostname := ""
	if job.input.Alert.CollectorID == q.host.CollectorID {
		hostname = q.host.Name
	}
	if metadata, found, err := q.store.Collector(ctx, job.input.Alert.CollectorID); err == nil && found && metadata.Hostname != "" {
		hostname = metadata.Hostname
	}
	job.input.Host.Name = hostname
	job.input.DashboardURL = cfg.Settings.PublicBaseURL
	return job, channel.Filter.Match(job.input.Alert.Severity, job.input.Alert.CollectorID, hostname, job.input.Target.Tags)
}

func (q *notificationQueue) runDelivery(ctx context.Context, queue *notificationDeliveryQueue) {
	q.runDeliveryWithUpdates(ctx, queue, nil)
}

func (q *notificationQueue) runDeliveryWithUpdates(ctx context.Context, queue *notificationDeliveryQueue, cardSent func(eventLogCardUpdate)) {
	// Client caches belong to one destination worker, so credential rotation never closes another worker's in-flight connection.
	sender := &notificationQueue{secrets: q.secrets, factory: q.factory, webhookFactory: q.webhookFactory, jsonWebhookFactory: q.jsonWebhookFactory, toast: q.toast, store: q.store, renderer: q.renderer, now: q.now, logger: q.logger, waiter: q.waiter, clients: queue.clients, clientsMu: &queue.clientsMu, cardActions: q.cardActions}
	sender.cardSent = cardSent
	queue.clientsMu.Lock()
	queue.lanes++
	queue.clientsMu.Unlock()
	defer func() {
		queue.clientsMu.Lock()
		defer queue.clientsMu.Unlock()
		if queue.lanes--; queue.lanes > 0 {
			return
		}
		for id, client := range queue.clients {
			_ = client.client.Close()
			delete(queue.clients, id)
		}
	}()
	for {
		item, ok := queue.next(ctx)
		if !ok {
			return
		}
		if item.update != nil {
			q.updateEventCard(ctx, sender, *item.update)
		} else {
			q.deliver(ctx, sender, item)
		}
		queue.done(item)
	}
}

func (q *notificationQueue) deliver(ctx context.Context, sender *notificationQueue, item notificationDelivery) {
	job, matched := q.prepareDelivery(ctx, item.job, item.notifier)
	defer func() {
		item.batch.mu.Lock()
		defer item.batch.mu.Unlock()
		item.batch.left--
		item.batch.matched = item.batch.matched || matched
		if item.batch.left == 0 && !item.batch.matched {
			q.logger.Info("All notification channels filtered out", "alert", item.job.input.Alert.ID)
		}
		if item.batch.left == 0 && item.job.finish != nil {
			item.job.finish()
		}
	}()
	if !matched {
		return
	}
	if policy, ok := q.store.(rules.NotificationPolicy); ok {
		decision, err := q.readDecision(ctx, policy, job.input.Alert)
		if err != nil {
			q.logger.Error("Notification policy unavailable; retaining durable event", "alert", job.input.Alert.ID)
			return
		}
		if decision.Suppressed != "" {
			return
		}
		if decision.Digest {
			job.frozen = nil
			job.input.Alert.Flapping, job.rule.Template = true, "digest"
			q.fillDigest(ctx, &job.input)
			job.input.Detail = "10 分钟内触发超过 3 次，已合并后续变化；连续稳定 10 分钟后恢复普通通知"
		}
	}
	sender.allow = func(ctx context.Context) (bool, error) {
		_, allowed := q.prepareDelivery(ctx, item.job, item.notifier)
		if allowed {
			if policy, ok := q.store.(rules.NotificationPolicy); ok {
				decision, err := q.readDecision(ctx, policy, job.input.Alert)
				return decision.Suppressed == "", err
			}
		}
		return allowed, nil
	}
	if err := sender.send(ctx, job, item.notifier); err != nil {
		q.logger.Error("Alert notification failed", "notifier", item.notifier.ID, "alert", job.input.Alert.ID)
	}
}
