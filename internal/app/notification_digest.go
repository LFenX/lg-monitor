package app

import (
	"context"

	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

type notificationDigestSource interface {
	DigestAlerts(context.Context, rules.Alert, int64) (store.AlertDigest, error)
}

func (q *notificationQueue) fillDigest(ctx context.Context, input *cards.Input) {
	value := *input
	value.Alerts, value.AlertsTotal = nil, 0
	input.Alerts, input.AlertsTotal = []cards.Input{value}, 1
	source, ok := q.store.(notificationDigestSource)
	if !ok {
		return
	}
	history, err := source.DigestAlerts(ctx, input.Alert, q.now().UnixMilli())
	if err != nil {
		q.logger.Warn("Digest history unavailable; retaining current alert", "alert", input.Alert.ID)
		return
	}
	if len(history.Alerts) == 0 {
		return
	}
	input.Alerts, input.AlertsTotal = make([]cards.Input, 0, len(history.Alerts)), history.Total
	for _, alert := range history.Alerts {
		input.Alerts = append(input.Alerts, cards.Input{Alert: alert, Rule: input.Rule, Target: input.Target, Host: input.Host, DashboardURL: input.DashboardURL})
	}
}
