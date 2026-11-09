package app

import (
	"context"
	"slices"
	"sync"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
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
type notificationDeliveryQueue struct {
	mu      sync.Mutex
	pending []notificationDelivery
	ready   chan struct{}
	closed  bool
}

func newNotificationDeliveryQueue() *notificationDeliveryQueue {
	return &notificationDeliveryQueue{ready: make(chan struct{}, 1)}
}
func (q *notificationDeliveryQueue) push(item notificationDelivery) {
	q.mu.Lock()
	q.pending = append(q.pending, item)
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default:
	}
}
func (q *notificationDeliveryQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default:
	}
}
func (q *notificationDeliveryQueue) next(ctx context.Context) (notificationDelivery, bool) {
	for {
		if ctx.Err() != nil {
			return notificationDelivery{}, false
		}
		q.mu.Lock()
		if len(q.pending) > 0 {
			item := q.pending[0]
			q.pending[0] = notificationDelivery{}
			q.pending = q.pending[1:]
			if len(q.pending) == 0 {
				q.pending = nil
			}
			q.mu.Unlock()
			return item, true
		}
		closed := q.closed
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
	if channel == nil || channel.Type != destination.Type {
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
	sender := &notificationQueue{secrets: q.secrets, factory: q.factory, webhookFactory: q.webhookFactory, jsonWebhookFactory: q.jsonWebhookFactory, toast: q.toast, store: q.store, renderer: q.renderer, now: q.now, logger: q.logger, waiter: q.waiter, clients: map[string]cachedClient{}, cardActions: q.cardActions}
	sender.cardSent = cardSent
	defer func() {
		for _, client := range sender.clients {
			_ = client.client.Close()
		}
	}()
	for {
		item, ok := queue.next(ctx)
		if !ok {
			return
		}
		if item.update != nil {
			q.updateEventCard(ctx, sender, *item.update)
			continue
		}
		q.deliver(ctx, sender, item)
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
			value := job.input
			value.Alerts = nil
			job.input.Alerts = []cards.Input{value}
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
