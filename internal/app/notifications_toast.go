package app

import (
	"strings"

	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func toastContent(input cards.Input) notify.Toast {
	state := "提示告警"
	if input.Alert.State == rules.StateResolved {
		state = "告警已恢复"
	} else if input.Alert.Severity == "critical" {
		state = "严重告警"
	} else if input.Alert.Severity == "warning" {
		state = "警告"
	}
	host, target := input.Host.Name, input.Target.Name
	if host == "" {
		host = input.Alert.CollectorID
	}
	if target == "" {
		target = input.Alert.TargetID
	}
	return notify.Toast{Title: "lg-monitor · " + state, Message: strings.Join([]string{host + " · " + target, input.Alert.Summary}, "\n")}
}
