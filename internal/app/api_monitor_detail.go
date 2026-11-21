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
	"github.com/LFenX/lg-monitor/internal/store"
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

func healthGap(target config.Target) time.Duration {
	return store.HealthGap(target)
}

func confirmedHealthSource(target config.Target, from, until int64) string {
	return store.HealthSamplesSQL(target, from, until)
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
	if rule.Type != "cert_expiry" && rule.Type != "eventlog" {
		if duration, err := rule.For.Parse(); err == nil && duration < config.FailureConfirmation {
			hold = "1m"
		}
	}
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
	detail := api.MonitorDetail{Monitor: byID[id], Lineage: []api.MonitorRef{}, Children: []api.Monitor{}, Boards: []api.BoardRef{}, Latency: []api.LatencyPoint{}, Events: []api.MonitorEvent{}, Incidents: []api.MonitorIncident{}, Rules: []api.MonitorRuleDetail{}}
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
	params := map[string]any{"target": id, "since": since, "span": span, "until": now.UnixMilli()}
	source := healthSource(target)
	// Clamp before grouping so a sample exactly at now joins, rather than overwrites, the last bucket.
	slotIndex := `MIN(CAST((ts - :since) / :span AS INTEGER), ` + strconv.Itoa(detailSlots-1) + `)`
	normalized := confirmedHealthSource(target, since-(config.FailureConfirmation+healthGap(target)).Milliseconds(), now.UnixMilli())
	slotQuery := `SELECT ` + slotIndex + `, ` + stateWorstSQL + `, SUM(CASE WHEN state = 'up' THEN 1 ELSE 0 END), SUM(CASE WHEN state IN ('up', 'down') THEN 1 ELSE 0 END) FROM (` + normalized + `) WHERE ts >= :since GROUP BY 1`
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

	history, err := b.dayHistory(ctx, target, now)
	if err != nil {
		return nil, err
	}
	detail.Days, detail.Uptime7d, detail.Uptime30d = history.days, history.uptime7d, history.uptime30d
	if history.notice != "" {
		detail.HistoryNotice = history.notice
	}
	starts := []int64{history.first}

	if len(target.Probes) > 0 {
		detail.Latency, detail.LatencyAvg, err = b.latencyHistory(ctx, target, since, span)
		if err != nil {
			return nil, err
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

	if err := b.monitorEvents(ctx, &detail, target, params); err != nil {
		return nil, err
	}
	if err := b.monitorSegments(ctx, &detail, target, source, starts[0], now.UnixMilli()); err != nil {
		return nil, err
	}
	if err := b.monitorIncidents(ctx, &detail, targets); err != nil {
		return nil, err
	}
	return detail, nil
}

// monitorSegments compacts consecutive samples in SQL before the read-only row limit applies.
// Missing observations never become a continuous healthy interval.
func (b *APIBackend) monitorSegments(ctx context.Context, detail *api.MonitorDetail, target config.Target, source string, first, now int64) error {
	stale := healthGap(target)
	rangeSQL := store.MonitorSampleRangeSQL(source, `target_id = :target AND ts >= :first AND ts <= :until`)
	observations := `SELECT collector_id, ts, state FROM (` + rangeSQL + `)`
	if source == "probe_results" {
		observations = `SELECT collector_id, ts, CASE MIN(ok) WHEN 1 THEN 'up' ELSE 'down' END AS state FROM (` + rangeSQL + `) GROUP BY collector_id, ts`
	}
	query := `WITH observations AS (` + observations + `),
ordered AS (SELECT *, LAG(state) OVER (PARTITION BY collector_id ORDER BY ts) AS previous,
 LAG(ts) OVER (PARTITION BY collector_id ORDER BY ts) AS previous_ts,
 LEAD(ts, 1, :until) OVER (PARTITION BY collector_id ORDER BY ts) AS next_ts FROM observations),
runs AS (SELECT *, SUM(CASE WHEN state = previous AND ts - previous_ts <= :stale THEN 0 ELSE 1 END)
 OVER (PARTITION BY collector_id ORDER BY ts) AS run FROM ordered)
SELECT collector_id, MIN(ts), MAX(MIN(next_ts, ts + :stale)), state
FROM runs GROUP BY collector_id, run, state ORDER BY 2 DESC`
	rows, err := b.options.SQL.Query(ctx, bind(query, map[string]any{"target": target.ID, "first": first, "until": now, "stale": stale.Milliseconds()}))
	if err != nil {
		return err
	}
	if rows.Truncated {
		detail.HistoryNotice += "状态变化过多，精确时间仅展示最近读取的部分；未展示的时间不推断为正常。"
	}
	runs := make([]monitorRun, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		collector, _ := row[0].(string)
		from, _ := row[1].(int64)
		to, _ := row[2].(int64)
		state, _ := row[3].(string)
		runs = append(runs, monitorRun{collector: collector, from: from, to: to, state: state})
	}
	attachMonitorRuns(detail, runs, now)
	return nil
}

type monitorRun struct {
	collector, state string
	from, to         int64
}

func attachMonitorRuns(detail *api.MonitorDetail, runs []monitorRun, now int64) {
	type edge struct {
		ts        int64
		collector string
		state     string
		start     bool
	}
	var edges []edge
	var points []api.StatusSegment
	for _, run := range runs {
		collector, from, to, state := run.collector, run.from, run.to, run.state
		if state == "down" && to-from < config.FailureConfirmation.Milliseconds() {
			state = "up"
		}
		if from == to {
			if state == "down" {
				points = append(points, api.StatusSegment{From: from, To: to, State: state})
			}
			continue
		}
		edges = append(edges, edge{from, collector, state, true}, edge{to, collector, state, false})
	}
	slices.SortFunc(edges, func(a, c edge) int {
		if result := cmp.Compare(a.ts, c.ts); result != 0 {
			return result
		}
		if a.start != c.start {
			if a.start {
				return 1
			}
			return -1
		}
		return cmp.Compare(a.collector, c.collector)
	})
	active := map[string]string{}
	seen := map[string]bool{}
	var segments []api.StatusSegment
	var previous int64
	for index := 0; index < len(edges); {
		at := edges[index].ts
		state := ""
		for _, current := range active {
			if current == "down" {
				state = "down"
				break
			}
			if state == "" || current == "unknown" {
				state = current
			}
		}
		if state == "up" && len(active) < len(seen) {
			state = "unknown"
		}
		if state != "" && at > previous {
			if count := len(segments); count > 0 && segments[count-1].State == state && segments[count-1].To == previous {
				segments[count-1].To = at
			} else {
				segments = append(segments, api.StatusSegment{From: previous, To: at, State: state})
			}
		}
		for index < len(edges) && edges[index].ts == at {
			item := edges[index]
			if item.start {
				seen[item.collector] = true
				active[item.collector] = item.state
			} else {
				delete(active, item.collector)
			}
			index++
		}
		previous = at
	}
	segments = append(segments, points...)
	slices.SortFunc(segments, func(a, c api.StatusSegment) int { return cmp.Compare(a.From, c.From) })
	attach := func(slots []api.StatusSlot, end int64) {
		for index := range slots {
			to := end
			if index+1 < len(slots) {
				to = slots[index+1].From
			}
			for _, segment := range segments {
				if segment.To < slots[index].From || segment.From >= to && !(index == len(slots)-1 && segment.From == now && to == now) {
					continue
				}
				if segment.To == slots[index].From && segment.From != segment.To {
					continue
				}
				segment.From, segment.To = max(segment.From, slots[index].From), min(segment.To, to)
				slots[index].Segments = append(slots[index].Segments, segment)
			}
		}
	}
	attach(detail.Slots, now)
	attach(detail.Days, now)
}

// monitorEvents turns raw states into the moments that changed, plus alerts, newest first.
func (b *APIBackend) monitorEvents(ctx context.Context, detail *api.MonitorDetail, target config.Target, params map[string]any) error {
	names := map[string]string{}
	for _, host := range detail.Monitor.Hosts {
		names[host.CollectorID] = host.Hostname
	}
	since := params["since"].(int64)
	normalized := confirmedHealthSource(target, since-(config.FailureConfirmation+healthGap(target)).Milliseconds(), b.options.Now().UnixMilli())
	query := `SELECT collector_id, ts, state FROM (` + normalized + `) WHERE ts >= :since ORDER BY collector_id, ts`
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

const (
	incidentWindow = 7 * 24 * time.Hour
	incidentLimit  = 200
)

// monitorIncidents lists outages of this service and everything below it, newest first. A recovered outage
// counts once it lasted the confirmation minute; one still going counts at once, like the live badge.
func (b *APIBackend) monitorIncidents(ctx context.Context, detail *api.MonitorDetail, targets map[string]config.Target) error {
	now := b.options.Now().UnixMilli()
	first := now - incidentWindow.Milliseconds()
	members := append([]api.Monitor{detail.Monitor}, detail.Children...)
	alerts, err := b.options.Read.AlertHistory(ctx, "all", first)
	if err != nil {
		return err
	}
	incidents := []api.MonitorIncident{}
	for _, member := range members {
		target, ok := targets[member.ID]
		if !ok {
			continue
		}
		hosts := map[string]string{}
		for _, host := range member.Hosts {
			hosts[host.CollectorID] = cmp.Or(host.Hostname, host.CollectorID)
		}
		depth := 0
		if member.ID != detail.Monitor.ID {
			depth = member.Depth
		}
		runs, err := b.downRuns(ctx, target, first, now)
		if err != nil {
			return err
		}
		used := map[int]bool{}
		for _, run := range runs {
			ongoing := member.State == "down" && run.end >= now-healthGap(target).Milliseconds()
			if !ongoing && run.end-run.start < config.FailureConfirmation.Milliseconds() {
				continue
			}
			item := api.MonitorIncident{MonitorID: member.ID, MonitorName: member.Name, Depth: depth, Kind: "down", From: run.start, To: run.end, Reason: run.reason, Host: hosts[run.collector], Alerts: []api.IncidentAlert{}}
			if ongoing {
				item.To = 0
			}
			item.DurationMS = cmp.Or(item.To, now) - item.From
			for index, alert := range alerts {
				if alert.TargetID == member.ID && alert.StartedAt >= item.From-config.FailureConfirmation.Milliseconds() && alert.StartedAt <= cmp.Or(item.To, now) {
					item.Alerts = append(item.Alerts, api.IncidentAlert{StartedAt: alert.StartedAt, ResolvedAt: alert.ResolvedAt, Severity: alert.Severity, Summary: cmp.Or(alert.Summary, "告警触发")})
					used[index] = true
				}
			}
			incidents = append(incidents, item)
		}
		// Alerts with no outage around them (a certificate about to expire, say) still belong in the history.
		for index, alert := range alerts {
			if alert.TargetID != member.ID || used[index] || alert.StartedAt < first {
				continue
			}
			end := alert.ResolvedAt
			incidents = append(incidents, api.MonitorIncident{MonitorID: member.ID, MonitorName: member.Name, Depth: depth, Kind: "alert", From: alert.StartedAt, To: end, DurationMS: cmp.Or(end, now) - alert.StartedAt, Reason: cmp.Or(alert.Summary, "告警触发"), Host: hosts[alert.CollectorID], Alerts: []api.IncidentAlert{{StartedAt: alert.StartedAt, ResolvedAt: alert.ResolvedAt, Severity: alert.Severity, Summary: cmp.Or(alert.Summary, "告警触发")}}})
		}
	}
	slices.SortStableFunc(incidents, func(a, c api.MonitorIncident) int { return cmp.Compare(c.From, a.From) })
	detail.Incidents = incidents[:min(len(incidents), incidentLimit)]
	return nil
}

type downRun struct {
	collector, reason string
	start, end        int64
}

// downRuns groups raw samples into runs per computer, like the timeline, and keeps the failing ones with the
// reason recorded when each began.
func (b *APIBackend) downRuns(ctx context.Context, target config.Target, first, now int64) ([]downRun, error) {
	source := healthSource(target)
	rangeSQL := store.MonitorSampleRangeSQL(source, `target_id = :target AND ts >= :first AND ts <= :until`)
	observations := `SELECT collector_id, ts, state, json_extract(detail, '$.reason') AS reason FROM (` + rangeSQL + `)`
	if source == "probe_results" {
		observations = `SELECT collector_id, ts, CASE MIN(ok) WHEN 1 THEN 'up' ELSE 'down' END AS state, MAX(COALESCE(error, '')) AS reason FROM (` + rangeSQL + `) GROUP BY collector_id, ts`
	}
	query := `WITH observations AS (` + observations + `),
ordered AS (SELECT *, LAG(state) OVER (PARTITION BY collector_id ORDER BY ts) AS previous,
 LAG(ts) OVER (PARTITION BY collector_id ORDER BY ts) AS previous_ts,
 LEAD(ts, 1, :until) OVER (PARTITION BY collector_id ORDER BY ts) AS next_ts FROM observations),
runs AS (SELECT *, SUM(CASE WHEN state = previous AND ts - previous_ts <= :stale THEN 0 ELSE 1 END)
 OVER (PARTITION BY collector_id ORDER BY ts) AS run FROM ordered),
opened AS (SELECT *, FIRST_VALUE(reason) OVER (PARTITION BY collector_id, run ORDER BY ts) AS opening FROM runs WHERE state = 'down')
SELECT collector_id, MIN(ts), MAX(MIN(next_ts, ts + :stale)), MAX(COALESCE(opening, ''))
FROM opened GROUP BY collector_id, run ORDER BY 2 DESC LIMIT ` + strconv.Itoa(incidentLimit)
	rows, err := b.options.SQL.Query(ctx, bind(query, map[string]any{"target": target.ID, "first": first, "until": now, "stale": healthGap(target).Milliseconds()}))
	if err != nil {
		return nil, err
	}
	runs := make([]downRun, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		var run downRun
		run.collector, _ = row[0].(string)
		start, _ := number(row[1])
		end, _ := number(row[2])
		run.start, run.end = int64(start), int64(end)
		run.reason, _ = row[3].(string)
		runs = append(runs, run)
	}
	return runs, nil
}

type dayHistory struct {
	days                []api.StatusSlot
	uptime7d, uptime30d *float64
	first               int64
	notice              string
}

// dayHistory is the 30-day bar and its 7/30-day availability, shared by the detail page and the cards that show them.
func (b *APIBackend) dayHistory(ctx context.Context, target config.Target, now time.Time) (dayHistory, error) {
	var result dayHistory
	// Days start at local midnight, the way people read "today" and "yesterday".
	local := now.Local()
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	first := today.AddDate(0, 0, -(detailDays - 1))
	result.days = make([]api.StatusSlot, detailDays)
	starts := make([]int64, detailDays+1)
	for index := range result.days {
		starts[index] = first.AddDate(0, 0, index).UnixMilli()
		result.days[index].From = starts[index]
	}
	starts[detailDays] = today.AddDate(0, 0, 1).UnixMilli()
	normalizedDays := confirmedHealthSource(target, starts[0]-(config.FailureConfirmation+healthGap(target)).Milliseconds(), now.UnixMilli())
	dayQuery := `WITH confirmed_raw AS (` + normalizedDays + `), raw_start AS (SELECT MIN(ts)/3600000*3600000 AS first FROM confirmed_raw)
SELECT ts, CASE WHEN confirmation_ms<60000 AND worst=0 THEN 0 ELSE up END,
 CASE WHEN confirmation_ms<60000 AND worst=0 THEN 0 ELSE total END,
 CASE WHEN confirmation_ms<60000 AND worst=0 THEN 1 ELSE worst END,
 CASE WHEN confirmation_ms<60000 AND worst=0 THEN 1 ELSE 0 END
FROM status_hour WHERE target_id = :target AND ts >= :first AND ts<COALESCE((SELECT first FROM raw_start),9223372036854775807)
UNION ALL SELECT ts/3600000*3600000, SUM(CASE WHEN state='up' THEN 1 ELSE 0 END), SUM(CASE WHEN state IN ('up','down') THEN 1 ELSE 0 END), ` + stateWorstSQL + `,0 FROM confirmed_raw WHERE ts>=:first GROUP BY 1`
	rows, err := b.options.SQL.Query(ctx, bind(dayQuery, map[string]any{"target": target.ID, "first": starts[0]}))
	if err != nil {
		return result, err
	}
	dayUp, dayAll := make([]float64, detailDays), make([]float64, detailDays)
	dayWorst := make([]int64, detailDays)
	for index := range dayWorst {
		dayWorst[index] = -1
	}
	for _, row := range rows.Rows {
		ts, _ := row[0].(int64)
		if legacy, _ := row[4].(int64); legacy > 0 {
			result.notice = "部分旧汇总缺少持续时长，未计为异常；有原始采样的时段已按 1 分钟重算。"
		}
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
	for index := range result.days {
		if dayWorst[index] >= 0 {
			result.days[index].State = worstName[dayWorst[index]]
		}
		result.days[index].Uptime = percent(dayUp[index], dayAll[index])
		up30, all30 = up30+dayUp[index], all30+dayAll[index]
		if index >= detailDays-7 {
			up7, all7 = up7+dayUp[index], all7+dayAll[index]
		}
	}
	result.uptime7d, result.uptime30d, result.first = percent(up7, all7), percent(up30, all30), starts[0]
	return result, nil
}

// latencyHistory averages successful probes per 15-minute slot over the last day.
func (b *APIBackend) latencyHistory(ctx context.Context, target config.Target, since, span int64) ([]api.LatencyPoint, *float64, error) {
	points := []api.LatencyPoint{}
	rows, err := b.options.SQL.Query(ctx, bind(`SELECT CAST((ts - :since) / :span AS INTEGER), AVG(latency_ms), COUNT(*) FROM probe_results WHERE target_id = :target AND ok = 1 AND ts >= :since GROUP BY 1 ORDER BY 1`, map[string]any{"target": target.ID, "since": since, "span": span}))
	if err != nil {
		return points, nil, err
	}
	var sum, count float64
	for _, row := range rows.Rows {
		index, _ := row[0].(int64)
		average, _ := number(row[1])
		samples, _ := number(row[2])
		if index = min(index, detailSlots-1); index >= 0 {
			points = append(points, api.LatencyPoint{TS: since + index*span + span/2, MS: average})
			sum, count = sum+average*samples, count+samples
		}
	}
	if count == 0 {
		return points, nil, nil
	}
	average := sum / count
	return points, &average, nil
}
