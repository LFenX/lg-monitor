package app

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

type diagnosticEventSource interface {
	DiagnosticEvents(context.Context, rules.Alert) ([]collector.EventLogRecord, error)
}
type eventLogCardUpdate struct {
	job              notificationJob
	notifier         config.Notifier
	messageID        string
	sentAt, renderAt time.Time
	end              time.Time
}

func (q *notificationQueue) eventChanges() <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.eventChanged == nil {
		q.eventChanged = make(chan struct{})
	}
	return q.eventChanged
}

func (q *notificationQueue) waitEventChange(ctx context.Context, delay time.Duration, changed <-chan struct{}) error {
	if delay <= 0 {
		return nil
	}
	if q.waiter == nil {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-changed:
			return nil
		}
	}
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- q.waiter.Wait(waitCtx, delay) }()
	select {
	case err := <-done:
		return err
	case <-changed:
		cancel()
		<-done
		return nil
	case <-ctx.Done():
		cancel()
		<-done
		return ctx.Err()
	}
}

func (q *notificationQueue) eventDeadline(ctx context.Context, alert rules.Alert, after time.Duration) time.Time {
	offset := int64(0)
	if metadata, found, err := q.store.Collector(ctx, alert.CollectorID); err == nil && found {
		offset = metadata.ClockOffsetMS
	}
	return time.UnixMilli(alert.SourceTS).Add(time.Duration(offset) * time.Millisecond).Add(after)
}

func initialEventNotification(job notificationJob) bool {
	alert := job.input.Alert
	return !job.historical && alert.SourceTS > 0 && alert.State == rules.StateFiring && !alert.Flapping && (job.rule.Type == "process_absent" || job.rule.Type == "service_stopped") && !strings.HasPrefix(alert.NotificationKey, "resume:") && !strings.HasPrefix(alert.NotificationKey, "renotify:")
}

func (q *notificationQueue) prepareEventDelivery(ctx context.Context, item notificationDelivery, queue *notificationDeliveryQueue, pending *sync.WaitGroup) {
	alert := item.job.input.Alert
	if alert.SourceTS <= 0 || alert.State != rules.StateFiring || item.job.rule.Type != "process_absent" && item.job.rule.Type != "service_stopped" {
		queue.push(item)
		return
	}
	selected := item.job.rule.Template
	if selected == "" {
		selected = item.notifier.Template
	}
	frozen, err := q.renderer.Freeze(selected)
	source, known := q.store.(diagnosticEventSource)
	if err != nil || !known || !frozen.UsesEvents() || item.job.input.Alert.SourceTS <= 0 || item.job.input.Alert.State != rules.StateFiring {
		queue.push(item)
		return
	}
	item.job.frozen = frozen
	changed := q.eventChanges()
	events, err := source.DiagnosticEvents(ctx, item.job.input.Alert)
	if err != nil {
		q.logger.Warn("Diagnostic event query failed; sending alert without waiting")
		queue.push(item)
		return
	}
	item.job.input.Events = events
	deadline := q.eventDeadline(ctx, item.job.input.Alert, 8*time.Second)
	if len(events) > 0 || !initialEventNotification(item.job) || !q.now().Before(deadline) {
		queue.push(item)
		return
	}
	pending.Add(1)
	go func() {
		defer pending.Done()
		for {
			if err := q.waitEventChange(ctx, deadline.Sub(q.now()), changed); err != nil {
				return
			}
			changed = q.eventChanges()
			events, err := source.DiagnosticEvents(ctx, item.job.input.Alert)
			if err != nil {
				q.logger.Warn("Diagnostic event query failed; ending the optional wait")
			}
			item.job.input.Events = events
			if err != nil || len(events) > 0 || !q.now().Before(deadline) {
				if ctx.Err() == nil {
					queue.push(item)
				}
				return
			}
		}
	}()
}

func (q *notificationQueue) scheduleEventUpdate(ctx context.Context, update eventLogCardUpdate, queue *notificationDeliveryQueue, running *sync.WaitGroup) {
	if ctx.Err() != nil || !initialEventNotification(update.job) || update.notifier.Type != "feishu_app" || update.job.frozen == nil || !update.job.frozen.UsesEvents() {
		return
	}
	update.end = q.eventDeadline(ctx, update.job.input.Alert, 60*time.Second)
	if !q.now().Before(update.end) {
		return
	}
	running.Add(1)
	go func() {
		defer running.Done()
		for q.now().Before(update.end) {
			if err := q.waitEventChange(ctx, update.end.Sub(q.now()), nil); err != nil {
				return
			}
		}
		if ctx.Err() == nil {
			queue.push(notificationDelivery{update: &update})
		}
	}()
}

func (q *notificationQueue) updateEventCard(ctx context.Context, sender *notificationQueue, update eventLogCardUpdate) {
	if ctx.Err() != nil || q.now().Sub(update.sentAt) > 14*24*time.Hour {
		return
	}
	if _, eligible := q.prepareDelivery(ctx, update.job, update.notifier); !eligible {
		return
	}
	source, known := q.store.(diagnosticEventSource)
	if !known {
		return
	}
	events, err := source.DiagnosticEvents(ctx, update.job.input.Alert)
	if err != nil {
		q.logger.Warn("Late diagnostic query failed; card update skipped")
		return
	}
	newEvent := false
	for _, event := range events {
		known := false
		for _, previous := range update.job.input.Events {
			if event.Channel == previous.Channel && event.RecordID == previous.RecordID && event.TS == previous.TS {
				known = true
				break
			}
		}
		newEvent = newEvent || !known
	}
	if !newEvent {
		return
	}
	update.job.input.Events = events
	card := update.job.frozen.RenderAt(update.job.input, update.renderAt)
	if card.Fallback || !cards.UpdateEnabled(card.JSON) {
		return
	}
	var app config.FeishuApp
	for _, candidate := range update.job.apps {
		if candidate.ID == update.notifier.App {
			app = candidate
			break
		}
	}
	client, err := sender.getClient(ctx, app)
	if err != nil {
		q.logger.Warn("Late diagnostic client unavailable; card update skipped")
		return
	}
	updater, ok := client.(feishu.CardUpdater)
	if !ok {
		return
	}
	if source, ok := q.store.(interface {
		NotificationPause(context.Context) (store.NotificationPause, error)
	}); ok {
		pause, err := source.NotificationPause(ctx)
		if err != nil || pause.Paused {
			return
		}
	}
	if err := updater.UpdateCard(ctx, update.messageID, card.JSON); err != nil {
		q.logger.Warn("Late diagnostic card update failed; no retry", "alert", update.job.input.Alert.ID)
	}
}
