package app

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/blueprint"
	"github.com/LFenX/lg-monitor/internal/config"
)

const (
	detailSlots   = 96
	detailSlot    = 15 * time.Minute
	detailDays    = 30
	detailEvents  = 50
	stateWorstSQL = `MIN(CASE state WHEN 'down' THEN 0 WHEN 'up' THEN 2 ELSE 1 END)`
)

var worstName = map[int64]string{0: "down", 1: "unknown", 2: "up"}

func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	}
	return 0, false
}

func percent(up, total float64) *float64 {
	if total <= 0 {
		return nil
	}
	value := up * 100 / total
	return &value
}

// healthSource names the table that says whether a target is healthy, matching history().
func healthSource(target config.Target) string {
	switch {
	case target.Task != "":
		return "task_status"
	case blueprint.ProbeOnly(target):
		return "probe_results"
	}
	return "target_status"
}

func ruleText(rule config.Rule) string {
	hold := string(rule.For)
	if hold == "" || hold == "0s" {
		hold = "立即"
	} else {
		hold = "持续 " + hold + " 后"
	}
	switch rule.Type {
	case "process_absent":
		return "进程消失" + hold + "告警"
	case "port_closed":
		return "端口不再监听" + hold + "告警"
	case "probe_failed":
		return "无法访问" + hold + "告警"
	case "service_stopped":
		return "Windows 服务停止" + hold + "告警"
	case "cert_expiry":
		within := string(rule.Within)
		if within == "" {
			within = "14d"
		}
		return "HTTPS 证书剩余不到 " + strings.TrimSuffix(within, "d") + " 天时提醒"
	case "script_failed":
		return "脚本探测报错" + hold + "告警"
	}
	return cmp.Or(rule.Name, rule.ID)
}

func notifierName(cfg *config.Config, id string) string {
	for _, channel := range cfg.Notifiers {
		if channel.ID != id {
			continue
		}
		switch channel.Type {
		case "toast":
			return "本机 Windows 通知"
		case "feishu_app", "feishu_webhook":
			return "飞书 " + cmp.Or(channel.ChatName, channel.ID)
		}
		return "Webhook " + channel.ID
	}
	return id
}

// bind inlines named values: target IDs only contain [a-z0-9-] and the rest are integers,
// and the read-only query helper accepts only its panel parameters.
func bind(query string, values map[string]any) string {
	for name, value := range values {
		text := ""
		switch typed := value.(type) {
		case string:
			text = "'" + strings.ReplaceAll(typed, "'", "''") + "'"
		case int64:
			text = strconv.FormatInt(typed, 10)
		}
		query = strings.ReplaceAll(query, ":"+name, text)
	}
	return query
}

func (b *APIBackend) monitorDetail(ctx context.Context, cfg *config.Config, id string) (any, error) {
	targets := targetIndex(cfg)
	target, ok := targets[id]
	if !ok {
		return nil, missingAPI("监控 " + id + " 不存在，先调用 list_monitors 核对 ID")
	}
	monitors, err := b.monitors(ctx, cfg)
	if err != nil {
		return nil, err
	}
	byID := map[string]api.Monitor{}
	for _, item := range monitors {
		byID[item.ID] = item
	}
	detail := api.MonitorDetail{Monitor: byID[id], Lineage: []api.MonitorRef{}, Children: []api.Monitor{}, Boards: []api.BoardRef{}, Latency: []api.LatencyPoint{}, Events: []api.MonitorEvent{}, Rules: []api.MonitorRuleDetail{}}
	above := ancestors(targets, id)
	for index := len(above) - 1; index >= 0; index-- {
		parent := byID[above[index]]
		detail.Lineage = append(detail.Lineage, api.MonitorRef{ID: parent.ID, Name: parent.Name, Overall: parent.Overall})
	}
	if tree := boardItems(cfg, config.Board{Monitors: []string{id}}, monitors); len(tree) > 1 {
		detail.Children = tree[1:]
	}
	lineage := append([]string{id}, above...)
	for _, board := range cfg.Boards {
		if slices.ContainsFunc(lineage, func(item string) bool { return slices.Contains(board.Monitors, item) }) {
			detail.Boards = append(detail.Boards, api.BoardRef{ID: board.ID, Name: board.Name})
		}
	}
	for _, rule := range cfg.Rules {
		if rule.Target != id {
			continue
		}
		item := api.MonitorRuleDetail{ID: rule.ID, Type: rule.Type, Severity: rule.Severity, Text: ruleText(rule), Notify: []string{}}
		if rule.Type == "sql" && target.Task != "" {
			item.Text = "计划任务上次运行失败、被禁用或找不到时告警"
		}
		for _, channel := range rule.Notify {
			item.Notify = append(item.Notify, notifierName(cfg, channel))
		}
		detail.Rules = append(detail.Rules, item)
	}

	now := b.options.Now()
	since := now.Add(-detailSlots * detailSlot).UnixMilli()
	span := detailSlot.Milliseconds()
	params := map[string]any{"target": id, "since": since, "span": span}
	source := healthSource(target)
	slotQuery := `SELECT CAST((ts - :since) / :span AS INTEGER), ` + stateWorstSQL + `, SUM(CASE WHEN state = 'up' THEN 1 ELSE 0 END), SUM(CASE WHEN state IN ('up', 'down') THEN 1 ELSE 0 END) FROM ` + source + ` WHERE target_id = :target AND ts >= :since GROUP BY 1`
	if source == "probe_results" {
		slotQuery = `SELECT CAST((ts - :since) / :span AS INTEGER), MIN(CASE ok WHEN 1 THEN 2 ELSE 0 END), SUM(ok), COUNT(*) FROM probe_results WHERE target_id = :target AND ts >= :since GROUP BY 1`
	}
	detail.Slots = make([]api.StatusSlot, detailSlots)
	for index := range detail.Slots {
		detail.Slots[index].From = since + int64(index)*span
	}
	rows, err := b.options.SQL.Query(ctx, bind(slotQuery, params))
	if err != nil {
		return nil, err
	}
	var up24, all24 float64
	for _, row := range rows.Rows {
		index, _ := row[0].(int64)
		worst, _ := row[1].(int64)
		up, _ := number(row[2])
		all, _ := number(row[3])
		// A sample taken exactly now belongs to the newest slot.
		index = min(index, detailSlots-1)
		if index < 0 {
			continue
		}
		detail.Slots[index].State, detail.Slots[index].Uptime = worstName[worst], percent(up, all)
		up24, all24 = up24+up, all24+all
	}
	detail.Uptime24h = percent(up24, all24)

	// Days start at local midnight, the way people read "today" and "yesterday".
	local := now.Local()
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	first := today.AddDate(0, 0, -(detailDays - 1))
	detail.Days = make([]api.StatusSlot, detailDays)
	starts := make([]int64, detailDays+1)
	for index := range detail.Days {
		starts[index] = first.AddDate(0, 0, index).UnixMilli()
		detail.Days[index].From = starts[index]
	}
	starts[detailDays] = today.AddDate(0, 0, 1).UnixMilli()
	dayQuery := `SELECT ts, up, total, worst FROM status_hour WHERE target_id = :target AND ts >= :first`
	if source == "probe_results" {
		dayQuery = `SELECT ts, successes, samples, CASE WHEN successes < samples THEN 0 ELSE 2 END FROM probe_hour WHERE target_id = :target AND ts >= :first`
	}
	rows, err = b.options.SQL.Query(ctx, bind(dayQuery, map[string]any{"target": id, "first": starts[0]}))
	if err != nil {
		return nil, err
	}
	dayUp, dayAll := make([]float64, detailDays), make([]float64, detailDays)
	dayWorst := make([]int64, detailDays)
	for index := range dayWorst {
		dayWorst[index] = -1
	}
	for _, row := range rows.Rows {
		ts, _ := row[0].(int64)
		up, _ := number(row[1])
		all, _ := number(row[2])
		worst, _ := row[3].(int64)
		index, found := slices.BinarySearch(starts, ts)
		if !found {
			index--
		}
		if index < 0 || index >= detailDays {
			continue
		}
		dayUp[index], dayAll[index] = dayUp[index]+up, dayAll[index]+all
		if dayWorst[index] < 0 || worst < dayWorst[index] {
			dayWorst[index] = worst
		}
	}
	var up7, all7, up30, all30 float64
	for index := range detail.Days {
		if dayWorst[index] >= 0 {
			detail.Days[index].State = worstName[dayWorst[index]]
		}
		detail.Days[index].Uptime = percent(dayUp[index], dayAll[index])
		up30, all30 = up30+dayUp[index], all30+dayAll[index]
		if index >= detailDays-7 {
			up7, all7 = up7+dayUp[index], all7+dayAll[index]
		}
	}
	detail.Uptime7d, detail.Uptime30d = percent(up7, all7), percent(up30, all30)

	if len(target.Probes) > 0 {
		rows, err = b.options.SQL.Query(ctx, bind(`SELECT CAST((ts - :since) / :span AS INTEGER), AVG(latency_ms), COUNT(*) FROM probe_results WHERE target_id = :target AND ok = 1 AND ts >= :since GROUP BY 1 ORDER BY 1`, params))
		if err != nil {
			return nil, err
		}
		var sum, count float64
		for _, row := range rows.Rows {
			index, _ := row[0].(int64)
			average, _ := number(row[1])
			samples, _ := number(row[2])
			if index = min(index, detailSlots-1); index >= 0 {
				detail.Latency = append(detail.Latency, api.LatencyPoint{TS: since + index*span + span/2, MS: average})
				sum, count = sum+average*samples, count+samples
			}
		}
		if count > 0 {
			average := sum / count
			detail.LatencyAvg = &average
		}
		rows, err = b.options.SQL.Query(ctx, bind(`SELECT latency_ms, (SELECT tls_not_after FROM probe_results c WHERE c.target_id = :target AND c.ts >= :since AND c.tls_not_after IS NOT NULL ORDER BY c.ts DESC LIMIT 1) FROM probe_results WHERE target_id = :target AND ok = 1 AND ts >= :since ORDER BY ts DESC LIMIT 1`, params))
		if err != nil {
			return nil, err
		}
		if len(rows.Rows) == 1 {
			if latest, ok := number(rows.Rows[0][0]); ok {
				detail.LatencyNow = &latest
			}
			detail.CertExpiresAt, _ = rows.Rows[0][1].(int64)
		}
	}

	if err := b.monitorEvents(ctx, &detail, source, params); err != nil {
		return nil, err
	}
	return detail, nil
}

// monitorEvents turns raw states into the moments that changed, plus alerts, newest first.
func (b *APIBackend) monitorEvents(ctx context.Context, detail *api.MonitorDetail, source string, params map[string]any) error {
	names := map[string]string{}
	for _, host := range detail.Monitor.Hosts {
		names[host.CollectorID] = host.Hostname
	}
	query := `SELECT collector_id, ts, state FROM ` + source + ` WHERE target_id = :target AND ts >= :since ORDER BY collector_id, ts`
	if source == "probe_results" {
		query = `SELECT collector_id, ts, CASE MIN(ok) WHEN 1 THEN 'up' ELSE 'down' END FROM probe_results WHERE target_id = :target AND ts >= :since GROUP BY collector_id, ts ORDER BY collector_id, ts`
	}
	rows, err := b.options.SQL.Query(ctx, bind(query, params))
	if err != nil {
		return err
	}
	texts := map[string]string{"up": "恢复正常", "down": "出现异常", "unknown": "状态未知"}
	previous := map[string]string{}
	var events []api.MonitorEvent
	for _, row := range rows.Rows {
		collectorID, _ := row[0].(string)
		ts, _ := row[1].(int64)
		state, _ := row[2].(string)
		last, seen := previous[collectorID]
		previous[collectorID] = state
		if !seen || last == state {
			continue
		}
		events = append(events, api.MonitorEvent{TS: ts, Kind: state, Text: texts[state], Host: names[collectorID]})
	}
	alerts, err := b.options.Read.AlertHistory(ctx, "all", b.options.Now().Add(-detailDays*24*time.Hour).UnixMilli())
	if err != nil {
		return err
	}
	for _, alert := range alerts {
		if alert.TargetID != detail.Monitor.ID {
			continue
		}
		events = append(events, api.MonitorEvent{TS: alert.StartedAt, Kind: "alert", Text: cmp.Or(alert.Summary, "告警触发"), Host: names[alert.CollectorID]})
		if alert.ResolvedAt > 0 {
			events = append(events, api.MonitorEvent{TS: alert.ResolvedAt, Kind: "resolved", Text: "告警恢复", Host: names[alert.CollectorID]})
		}
	}
	slices.SortStableFunc(events, func(a, c api.MonitorEvent) int { return cmp.Compare(c.TS, a.TS) })
	detail.Events = append(detail.Events, events[:min(len(events), detailEvents)]...)
	return nil
}
