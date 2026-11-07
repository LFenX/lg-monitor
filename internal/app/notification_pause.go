package app

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

type pausePersistence interface {
	PreparePauseResumes(context.Context) error
	PendingPauseNotifications(context.Context) ([]store.PauseNotification, error)
	CompletePauseNotification(context.Context, string, string) error
	NotificationPause(context.Context) (store.NotificationPause, error)
}

func (q *notificationQueue) advancePause(ctx context.Context) error {
	db, ok := q.store.(pausePersistence)
	if !ok {
		return nil
	}
	if err := db.PreparePauseResumes(ctx); err != nil {
		return err
	}
	state, err := db.NotificationPause(ctx)
	if err != nil {
		return err
	}
	if q.lastPause == nil || !reflect.DeepEqual(*q.lastPause, state) {
		if observer, ok := q.observer.(interface{ Pause(store.NotificationPause) }); ok {
			observer.Pause(state)
		}
		q.lastPause = &state
	}
	items, err := db.PendingPauseNotifications(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		key := item.Alert.ID + "\x00" + item.Alert.NotificationKey
		q.mu.Lock()
		busy := q.pauseFlights[key]
		q.mu.Unlock()
		if busy {
			continue
		}
		if policy, ok := q.store.(rules.NotificationPolicy); ok {
			decision, err := q.readDecision(ctx, policy, item.Alert)
			if err != nil {
				return err
			}
			if decision.Suppressed != "" {
				if err := db.CompletePauseNotification(ctx, item.Alert.ID, item.Alert.NotificationKey); err != nil {
					return err
				}
				continue
			}
		}
		job, jobErr := q.job(item.Alert)
		if jobErr == nil {
			channels := job.notifiers[:0]
			for _, channel := range job.notifiers {
				if slices.Contains(item.Successful, channel.ID) || slices.Contains(item.Skipped, channel.ID) || (item.Alert.State == rules.StateResolved && !slices.Contains(item.Allowed, channel.ID)) {
					continue
				}
				if _, allowed := q.prepareDelivery(ctx, job, channel); allowed {
					channels = append(channels, channel)
				}
			}
			job.notifiers = channels
		}
		if jobErr != nil || len(job.notifiers) == 0 {
			if err := db.CompletePauseNotification(ctx, item.Alert.ID, item.Alert.NotificationKey); err != nil {
				return err
			}
			continue
		}
		q.mu.Lock()
		if q.pauseFlights == nil {
			q.pauseFlights = map[string]bool{}
		}
		q.pauseFlights[key] = true
		q.mu.Unlock()
		job.finish = func() { q.mu.Lock(); delete(q.pauseFlights, key); q.mu.Unlock() }
		select {
		case q.jobs <- job:
		case <-ctx.Done():
			job.finish()
			return fmt.Errorf("暂停补发入队已取消：%w", ctx.Err())
		default:
			job.finish()
			return ErrNotificationQueueFull
		}
	}
	return nil
}
