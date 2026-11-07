package notify

import (
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func WebhookPayload(input cards.Input, now time.Time, test bool) config.WebhookPayload {
	timestamp := func(value int64) *config.WebhookTimestamp {
		if value <= 0 {
			return nil
		}
		result := config.WebhookTimestamp(time.UnixMilli(value).UTC().Format("2006-01-02T15:04:05.000Z"))
		return &result
	}
	clip := func(value string) string {
		runes := []rune(value)
		if len(runes) > 2000 {
			return string(runes[:1999]) + "…"
		}
		return value
	}
	kind := "alert.firing"
	if input.Alert.State == rules.StateResolved {
		kind = "alert.resolved"
	}
	if strings.HasPrefix(input.Alert.NotificationKey, "renotify:") {
		kind = "alert.renotify"
	}
	if test {
		kind = "alert.test"
		input.Alert.Summary = "这是测试消息：" + input.Alert.Summary
	}
	at := input.Alert.NotificationAt
	if at <= 0 {
		at = now.UnixMilli()
	}
	var dashboard *string
	if input.DashboardURL != "" {
		value := input.DashboardURL
		dashboard = &value
	}
	tags := append([]string{}, input.Target.Tags...)
	hostname := input.Host.Name
	if hostname == "" {
		hostname = input.Alert.CollectorID
	}
	return config.WebhookPayload{Type: kind, Timestamp: config.WebhookTimestamp(time.UnixMilli(at).UTC().Format("2006-01-02T15:04:05.000Z")), Data: config.WebhookData{
		Version:   1,
		Alert:     config.WebhookAlert{ID: input.Alert.ID, State: input.Alert.State, Severity: input.Alert.Severity, Summary: clip(input.Alert.Summary), Detail: clip(input.Detail), Value: clip(input.Value), StartedAt: timestamp(input.Alert.StartedAt), ResolvedAt: timestamp(input.Alert.ResolvedAt), Acknowledged: input.Alert.AcknowledgedAt > 0, Flapping: input.Alert.Flapping},
		Rule:      config.WebhookRule{ID: input.Alert.RuleID, Name: input.Rule.Name, Type: input.Rule.Type},
		Target:    config.WebhookTarget{ID: input.Alert.TargetID, Name: input.Target.Name, Tags: tags},
		Collector: config.WebhookCollector{ID: input.Alert.CollectorID, Hostname: hostname}, DashboardURL: dashboard,
	}}
}
