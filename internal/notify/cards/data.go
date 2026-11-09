package cards

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
)

type Host struct {
	Name        string
	CollectorID string
}

type Input struct {
	Alert        rules.Alert
	Rule         config.Rule
	Target       config.Target
	Host         Host
	Detail       string
	Value        string
	DashboardURL string
	Processes    []collector.Process
	LogTail      string
	Diagnosis    string
	Alerts       []Input
	AlertsTotal  int
	Events       []collector.EventLogRecord
	Actions      []CallbackAction
}

type CallbackAction struct {
	Text  string
	Value json.RawMessage
}

func (r *Renderer) data(in Input, now time.Time, limit int) map[string]any {
	end := now.UnixMilli()
	if in.Alert.State == rules.StateResolved && in.Alert.ResolvedAt > 0 {
		end = in.Alert.ResolvedAt
	}
	var elapsed time.Duration
	if in.Alert.StartedAt > 0 && end > in.Alert.StartedAt {
		elapsed = time.Duration(end-in.Alert.StartedAt) * time.Millisecond
	}
	icon, text := "🔵", "提示"
	if in.Alert.State == rules.StateResolved {
		icon, text = "✅", "已恢复"
	} else if in.Alert.Severity == "critical" {
		icon, text = "🔴", "严重"
	} else if in.Alert.Severity == "warning" {
		icon, text = "🟠", "警告"
	}
	host := in.Host
	if host.CollectorID == "" {
		host.CollectorID = in.Alert.CollectorID
	}
	if host.Name == "" {
		host.Name = host.CollectorID
	}
	processes := make([]any, 0, len(in.Processes))
	for _, p := range in.Processes {
		processes = append(processes, map[string]any{
			"PID": p.PID, "Name": p.Name, "CPUPercent": p.CPUPercent, "RSS": p.RSS,
			"Memory": fmt.Sprintf("%.1f MiB", float64(p.RSS)/(1024*1024)),
		})
	}
	list := make([]any, 0, min(len(in.Alerts), limit))
	for _, item := range in.Alerts[:min(len(in.Alerts), limit)] {
		item.Alerts = nil
		list = append(list, r.data(item, now, 0))
	}
	var certificate map[string]any
	if in.Rule.Type == "cert_expiry" && in.Alert.Cert != nil {
		certificate = map[string]any{
			"notAfter": r.formatTime(in.Alert.Cert.NotAfter),
			"subject":  in.Alert.Cert.Subject,
			"daysLeft": in.Alert.Cert.DaysLeft(now),
		}
	}
	events := make([]any, 0, min(len(in.Events), 5))
	for _, event := range in.Events[max(0, len(in.Events)-5):] {
		message := []rune(event.Message)
		if len(message) > 200 {
			message = message[:200]
		}
		events = append(events, map[string]any{"Time": r.formatTime(event.TS), "Provider": event.Provider, "EventID": event.EventID, "Subject": event.Subject, "Message": string(message)})
	}
	return map[string]any{
		"Alert": map[string]any{
			"ID": in.Alert.ID, "State": in.Alert.State, "Severity": in.Alert.Severity,
			"Summary": in.Alert.Summary, "Detail": in.Detail,
			"StartedAt": r.formatTime(in.Alert.StartedAt), "ResolvedAt": r.formatTime(in.Alert.ResolvedAt),
			"Duration": chineseDuration(elapsed),
		},
		"Rule":   map[string]any{"ID": in.Rule.ID, "Name": first(in.Rule.Name, in.Rule.ID)},
		"Target": map[string]any{"ID": in.Target.ID, "Name": first(in.Target.Name, in.Target.ID), "Tags": in.Target.Tags},
		"Host":   map[string]any{"Name": host.Name, "CollectorID": host.CollectorID},
		"Value":  in.Value, "SeverityIcon": icon, "SeverityText": text,
		"DashboardURL": in.DashboardURL, "Processes": processes,
		"LogTail": in.LogTail, "Diagnosis": in.Diagnosis, "Alerts": list,
		"Cert":   certificate,
		"Events": events,
	}
}

func (r *Renderer) diagnosticInput(in Input) Input {
	if in.LogTail == "" && len(in.Events) > 0 {
		var lines []string
		for _, event := range in.Events[max(0, len(in.Events)-5):] {
			lines = append(lines, fmt.Sprintf("%s · %s · 事件 %d · %s（正文未导出）", r.formatTime(event.TS), event.Provider, event.EventID, event.Subject))
		}
		in.LogTail = strings.Join(lines, "\n")
	}
	return in
}

func (r *Renderer) formatTime(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).In(r.location).Format("2006-01-02 15:04:05")
}

func chineseDuration(d time.Duration) string {
	seconds := max(int64(d/time.Second), 0)
	if seconds == 0 {
		return "0秒"
	}
	var out strings.Builder
	for _, unit := range []struct {
		size int64
		name string
	}{{86400, "天"}, {3600, "小时"}, {60, "分"}, {1, "秒"}} {
		if count := seconds / unit.size; count > 0 {
			out.WriteString(strconv.FormatInt(count, 10))
			out.WriteString(unit.name)
			seconds %= unit.size
		}
	}
	return out.String()
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func head(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:max(n, 0)]) + "…"
}

func tail(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return "…" + string(runes[len(runes)-max(n, 0):])
}

func Sample(state string) Input {
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).UnixMilli()
	in := Input{
		Alert:  rules.Alert{ID: "sample-alert", RuleID: "sample-rule", CollectorID: "sample-host", TargetID: "demo", State: state, Severity: "critical", StartedAt: start, Summary: "演示服务停止响应"},
		Rule:   config.Rule{ID: "sample-rule", Name: "演示服务可用性"},
		Target: config.Target{ID: "demo", Name: "演示服务", Tags: []string{"演示", "开发"}},
		Host:   Host{Name: "演示主机", CollectorID: "sample-host"},
		Detail: "连续探测失败，请检查进程和监听端口。", Value: "连接被拒绝",
		DashboardURL: "https://example.com/alerts/sample-alert",
		Processes:    []collector.Process{{PID: 1234, Name: "demo.exe", CPUPercent: 1.25, RSS: 24 * 1024 * 1024}},
		LogTail:      "演示日志：准备启动\n演示日志：连接失败",
		Diagnosis:    "检查应用日志和端口配置，再启动演示进程。",
	}
	if state == "resolved" {
		in.Alert.ResolvedAt = start + (5 * time.Minute).Milliseconds()
		in.Alert.Summary = "演示服务已恢复"
		in.Value = "200 OK"
	}
	if state == "digest" {
		in.Alerts = []Input{Sample("firing"), Sample("resolved")}
		third := Sample("firing")
		third.Alert.Severity = "warning"
		third.Target.Name = "另一演示服务"
		in.Alerts = append(in.Alerts, third)
	}
	return in
}
