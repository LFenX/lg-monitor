package app

import (
	"fmt"
	"strings"
	"time"

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
	lines := []string{host + " · " + target, input.Alert.Summary}
	if len(input.Events) > 0 {
		event := input.Events[len(input.Events)-1]
		message := []rune(event.Message)
		if len(message) > 200 {
			message = message[:200]
		}
		lines = append(lines, fmt.Sprintf("关联事件（UTC %s）：%s #%d %s\n%s", time.UnixMilli(event.TS).UTC().Format("2006-01-02 15:04:05"), event.Provider, event.EventID, event.Subject, string(message)))
	}
	return notify.Toast{Title: "lg-monitor · " + state, Message: strings.Join(lines, "\n")}
}
