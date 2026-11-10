package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/blueprint"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	"gopkg.in/yaml.v3"
)

// agentState holds what the intent-level operations remember between calls.
type agentState struct {
	mu        sync.Mutex
	seenAt    int64
	seenName  string
	savedAt   int64
	history   *monitorHistory
	historyAt time.Time
	snapshots map[string]processSnapshot
}

type processSnapshot struct {
	at   time.Time
	list collector.ProcessList
}

// processCacheTTL lets a page filter and paginate one snapshot without asking the collector again.
const processCacheTTL = 4 * time.Second

type agentSeen struct {
	At   int64  `json:"at"`
	Name string `json:"name"`
}

func (b *APIBackend) agentSeenPath() string {
	if b.options.Home == "" {
		return ""
	}
	return filepath.Join(b.options.Home, "data", "agent-seen.json")
}

// loadAgentSeen restores the last agent contact so the onboarding journey survives restarts.
func (b *APIBackend) loadAgentSeen() {
	path := b.agentSeenPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var seen agentSeen
	if json.Unmarshal(data, &seen) == nil && seen.At > 0 {
		b.agent.seenAt, b.agent.seenName, b.agent.savedAt = seen.At, seen.Name, seen.At
	}
}

func (b *APIBackend) noteAgent(ctx context.Context, r api.Request) {
	if r.Via != "mcp" {
		return
	}
	principal, _ := api.CurrentPrincipal(ctx)
	now := b.options.Now().UnixMilli()
	b.agent.mu.Lock()
	b.agent.seenAt, b.agent.seenName = now, principal.Name
	persist := now-b.agent.savedAt >= time.Minute.Milliseconds()
	if persist {
		b.agent.savedAt = now
	}
	b.agent.mu.Unlock()
	if path := b.agentSeenPath(); persist && path != "" {
		data, _ := json.Marshal(agentSeen{At: now, Name: principal.Name})
		if err := os.WriteFile(path, data, 0o600); err != nil {
			b.options.Logger.Warn("Agent contact time was not saved", "error", err)
		}
	}
}

func (b *APIBackend) baseURL(cfg *config.Config) string {
	if cfg.Settings.PublicBaseURL != "" {
		return strings.TrimRight(cfg.Settings.PublicBaseURL, "/")
	}
	return "http://" + cfg.Settings.HTTPAddress()
}

func encodeYAML(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("编码配置失败：%w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("完成配置编码失败：%w", err)
	}
	return out.Bytes(), nil
}

func (b *APIBackend) presences(ctx context.Context) ([]api.GuideCollector, error) {
	items, err := b.options.Collectors.Collectors(ctx)
	if err != nil {
		return nil, err
	}
	now := b.options.Now().UnixMilli()
	result := make([]api.GuideCollector, 0, len(items))
	for _, item := range items {
		result = append(result, api.GuideCollector{ID: item.ID, Hostname: item.Hostname, Online: item.Connected && now-item.LastSeen <= 30000})
	}
	slices.SortFunc(result, func(a, c api.GuideCollector) int { return strings.Compare(a.ID, c.ID) })
	return result, nil
}

// pickCollector resolves an optional ID or hostname; a single host needs no name at all.
func (b *APIBackend) pickCollector(ctx context.Context, name string) (api.GuideCollector, error) {
	hosts, err := b.presences(ctx)
	if err != nil {
		return api.GuideCollector{}, err
	}
	if name == "" {
		if len(hosts) == 1 {
			return hosts[0], nil
		}
		online := slices.DeleteFunc(slices.Clone(hosts), func(host api.GuideCollector) bool { return !host.Online })
		if len(online) == 1 {
			return online[0], nil
		}
		if len(hosts) == 0 {
			return api.GuideCollector{}, &api.Fault{Status: 409, Code: "no_collector", Message: "还没有任何采集器连接。请确认监控器以 --role both 运行，或在被监控电脑上完成 lgmon pair join"}
		}
		names := make([]string, 0, len(hosts))
		for _, host := range hosts {
			names = append(names, host.Hostname+"（"+host.ID+"）")
		}
		return api.GuideCollector{}, invalidAPI("有多台电脑，请用 collector 指定其中一台：" + strings.Join(names, "、"))
	}
	for _, host := range hosts {
		if host.ID == name || strings.EqualFold(host.Hostname, name) {
			return host, nil
		}
	}
	return api.GuideCollector{}, missingAPI("采集器 " + name + " 不存在，请先调用 start_here 查看可用的电脑")
}

func (b *APIBackend) guide(ctx context.Context, cfg *config.Config) (any, error) {
	principal, _ := api.CurrentPrincipal(ctx)
	hosts, err := b.presences(ctx)
	if err != nil {
		return nil, err
	}
	states, err := b.targets(ctx, cfg)
	if err != nil {
		return nil, err
	}
	firing, err := b.options.Read.AlertHistory(ctx, rules.StateFiring, 0)
	if err != nil {
		return nil, err
	}
	state := api.GuideState{Collectors: len(hosts), Monitors: len(cfg.Targets), FiringAlerts: len(firing), Dashboards: len(cfg.Dashboards), Notifiers: len(cfg.Notifiers)}
	for _, host := range hosts {
		if host.Online {
			state.Online++
		}
	}
	for _, item := range states {
		if item.State == "up" {
			state.Healthy++
		}
	}
	b.agent.mu.Lock()
	state.AgentSeenAt, state.AgentName = b.agent.seenAt, b.agent.seenName
	b.agent.mu.Unlock()
	base := b.baseURL(cfg)
	home, executable := b.options.Home, b.options.Executable
	connect := fmt.Sprintf("& %s agent connect --home %s", quotePowerShell(executable), quotePowerShell(home))
	feishu := false
	for _, channel := range cfg.Notifiers {
		if channel.Type != "toast" {
			feishu = true
		}
	}
	steps := []api.GuideStep{
		{ID: "collector", Title: "采集器已连接", Done: state.Online > 0, How: "监控器以 --role both 运行时会自带本机采集器；其他电脑运行 lgmon pair join 完成配对。"},
		{ID: "agent", Title: "让 Agent 接入", Done: state.AgentSeenAt > 0 || principal.Scope.Allows(api.ScopeConfig) && state.Monitors > 0, How: "在监控器电脑的 PowerShell 里运行：" + connect + "，然后把输出的配置交给你的 Agent。"},
		{ID: "monitor", Title: "添加第一个监控", Done: state.Monitors > 0, Tool: "add_monitor", How: "先用 explore 看这台电脑上在运行什么，再用 add_monitor 传入 name 加上 port、process、service 或 url 之一。一次调用会同时建好目标、告警规则、通知和面板。"},
		{ID: "dashboard", Title: "做一页面板", Done: len(cfg.Dashboards) > 0, Tool: "create_dashboard", How: "面板按内容分类：system（本机）、services（服务）、websites（网站）、alerts（告警）、windows（Windows 系统）。调用 create_dashboard 传 template 即可，不需要其他参数；把返回的 url 交给用户打开。"},
		{ID: "notify", Title: "把告警发到手机", Done: feishu, How: "默认只有本机 Windows 通知。需要飞书时，请用户在网页“通知 → 接入向导”里自己填写应用凭证，Agent 不接触密钥。"},
	}
	return api.Guide{
		Product: "lg-monitor：监控这台 Windows 电脑上的进程、服务、端口和网站，出面板并发告警。", Version: b.options.Version,
		Scope: principal.Scope, CanConfigure: principal.Scope.Allows(api.ScopeConfig),
		Install: api.GuideInstall{Executable: executable, Home: home, Personal: b.options.Personal, URL: base},
		State:   state, Collectors: hosts, Steps: steps,
		Workflow: []string{
			"1. start_here：了解现状和下一步（就是这次调用）。",
			"2. explore：看这台电脑上有哪些端口、服务、进程可以监控；q 可按名字搜索。",
			"3. add_monitor：一次调用完成监控；用 explore 返回的 suggest 作参数最稳妥。",
			"4. list_monitors：确认新监控的 state，unknown 表示还没采到第一轮数据，等十秒再看。",
			"5. create_dashboard：一类内容一页（system、services、websites、alerts、windows），不要为每个监控单独建面板；需要完全自定义时用 put_dashboard，它会实际执行每张卡片的查询并指出错在哪里。",
			"6. 把 url 交给用户，在浏览器里查看。",
		},
		Rules: []string{
			"只监控用户点名或确认过的对象；不停止、重启或修改任何已有的进程和服务。",
			"当前权限是 " + string(principal.Scope) + "；写操作需要 config。权限不足时把原因和 install.executable 的 agent connect 命令告诉用户，不要绕过。",
			"不索取、不填写任何密钥或令牌；飞书凭证由用户自己在网页里填写。",
			"工具返回 isError 时读取 error.message，它会说明怎么改；不要原样重试。",
		},
	}, nil
}

func quotePowerShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func summarizeMatch(target config.Target) []string {
	var parts []string
	probeOnly := blueprint.ProbeOnly(target)
	if target.Match.Port != nil {
		parts = append(parts, "端口 "+strconv.Itoa(*target.Match.Port))
	}
	if target.Match.Process != "" && !probeOnly {
		parts = append(parts, "进程 "+target.Match.Process)
	}
	if target.Match.Service != "" {
		parts = append(parts, "服务 "+target.Match.Service)
	}
	if target.Match.ExeDir != "" {
		parts = append(parts, "程序目录 "+target.Match.ExeDir)
	}
	for _, probe := range target.Probes {
		switch probe.Type {
		case "http":
			parts = append(parts, "网址 "+probe.URL)
		case "script":
			parts = append(parts, "脚本 "+probe.Script)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "自定义匹配条件")
	}
	return parts
}

const (
	beatCount = 48
	beatSpan  = 30 * time.Minute
	// historyTTL lets every open page share one 24-hour scan instead of repeating it on each refresh.
	historyTTL = 30 * time.Second
)

type monitorHistory struct {
	beats   map[string][]int // worst state per bucket: 0 down, 1 unknown, 2 up, -1 no data
	up, all map[string]int64
	latency map[string]float64
}

func (b *APIBackend) history(ctx context.Context, cfg *config.Config) (monitorHistory, error) {
	now := b.options.Now()
	b.agent.mu.Lock()
	cached, at := b.agent.history, b.agent.historyAt
	b.agent.mu.Unlock()
	if cached != nil && now.Sub(at) < historyTTL {
		return *cached, nil
	}
	result := monitorHistory{beats: map[string][]int{}, up: map[string]int64{}, all: map[string]int64{}, latency: map[string]float64{}}
	from := now.Add(-beatCount * beatSpan).UnixMilli()
	span := beatSpan.Milliseconds()
	probeOnly := map[string]bool{}
	for _, target := range cfg.Targets {
		probeOnly[target.ID] = blueprint.ProbeOnly(target)
	}
	add := func(target string, bucket, worst, up, all int64) {
		if bucket < 0 || bucket >= beatCount {
			return
		}
		row := result.beats[target]
		if row == nil {
			row = make([]int, beatCount)
			for i := range row {
				row[i] = -1
			}
			result.beats[target] = row
		}
		if row[bucket] == -1 || int(worst) < row[bucket] {
			row[bucket] = int(worst)
		}
		result.up[target] += up
		result.all[target] += all
	}
	read := func(query string, probes bool) error {
		rows, err := b.options.SQL.Query(ctx, query)
		if err != nil {
			return err
		}
		for _, row := range rows.Rows {
			target, _ := row[0].(string)
			if probeOnly[target] != probes {
				continue
			}
			bucket, _ := row[1].(int64)
			worst, _ := row[2].(int64)
			up, _ := row[3].(int64)
			all, _ := row[4].(int64)
			add(target, bucket, worst, up, all)
		}
		return nil
	}
	fromText, spanText := strconv.FormatInt(from, 10), strconv.FormatInt(span, 10)
	if err := read(`SELECT target_id, CAST((ts - `+fromText+`) / `+spanText+` AS INTEGER) AS b, MIN(CASE state WHEN 'down' THEN 0 WHEN 'up' THEN 2 ELSE 1 END) AS worst, SUM(CASE WHEN state = 'up' THEN 1 ELSE 0 END) AS up, SUM(CASE WHEN state IN ('up', 'down') THEN 1 ELSE 0 END) AS n FROM target_status WHERE ts >= `+fromText+` GROUP BY 1, 2`, false); err != nil {
		return result, err
	}
	if err := read(`SELECT target_id, CAST((ts - `+fromText+`) / `+spanText+` AS INTEGER) AS b, MIN(CASE ok WHEN 1 THEN 2 ELSE 0 END) AS worst, SUM(ok) AS up, COUNT(*) AS n FROM probe_results WHERE ts >= `+fromText+` GROUP BY 1, 2`, true); err != nil {
		return result, err
	}
	rows, err := b.options.SQL.Query(ctx, `SELECT target_id, AVG(latency_ms) FROM probe_results WHERE ok = 1 AND ts >= `+strconv.FormatInt(now.Add(-5*time.Minute).UnixMilli(), 10)+` GROUP BY 1`)
	if err != nil {
		return result, err
	}
	for _, row := range rows.Rows {
		target, _ := row[0].(string)
		switch value := row[1].(type) {
		case float64:
			result.latency[target] = value
		case int64:
			result.latency[target] = float64(value)
		}
	}
	b.agent.mu.Lock()
	b.agent.history, b.agent.historyAt = &result, now
	b.agent.mu.Unlock()
	return result, nil
}

func monitorKind(target config.Target) string {
	switch {
	case slices.ContainsFunc(target.Probes, func(probe config.Probe) bool { return probe.Type == "http" }):
		return "url"
	case target.Match.Service != "":
		return "service"
	case target.Match.Port != nil:
		return "port"
	case target.Match.Process != "" || target.Match.Cmdline != "":
		return "process"
	}
	return "other"
}

type probeState struct {
	ok     bool
	reason string
	ts     int64
}

// probeStates reads the latest probe outcome per host for URL monitors, whose matched process says nothing about the site.
func (b *APIBackend) probeStates(ctx context.Context, cfg *config.Config) (map[[2]string]probeState, error) {
	var ids []string
	for _, target := range cfg.Targets {
		if blueprint.ProbeOnly(target) {
			ids = append(ids, "'"+target.ID+"'")
		}
	}
	result := map[[2]string]probeState{}
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := b.options.SQL.Query(ctx, `SELECT p.collector_id, p.target_id, p.ok, COALESCE(NULLIF(p.error, ''), NULLIF(p.message, ''), '') AS reason, p.ts FROM probe_results p WHERE p.target_id IN (`+strings.Join(ids, ",")+`) AND p.ts = (SELECT max(n.ts) FROM probe_results n WHERE n.collector_id = p.collector_id AND n.target_id = p.target_id AND n.probe = p.probe)`)
	if err != nil {
		return nil, err
	}
	for _, row := range rows.Rows {
		collectorID, _ := row[0].(string)
		targetID, _ := row[1].(string)
		ok, _ := row[2].(int64)
		reason, _ := row[3].(string)
		ts, _ := row[4].(int64)
		key := [2]string{collectorID, targetID}
		previous, seen := result[key]
		state := probeState{ok: ok == 1, reason: reason, ts: ts}
		if seen && !previous.ok {
			// Any failing probe makes the site unhealthy.
			continue
		}
		if !seen || !state.ok || ts > previous.ts {
			result[key] = state
		}
	}
	return result, nil
}

func (b *APIBackend) monitors(ctx context.Context, cfg *config.Config) ([]api.Monitor, error) {
	states, err := b.targets(ctx, cfg)
	if err != nil {
		return nil, err
	}
	hosts, err := b.presences(ctx)
	if err != nil {
		return nil, err
	}
	firing, err := b.options.Read.AlertHistory(ctx, rules.StateFiring, 0)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, host := range hosts {
		names[host.ID] = host.Hostname
	}
	probes, err := b.probeStates(ctx, cfg)
	if err != nil {
		return nil, err
	}
	past, err := b.history(ctx, cfg)
	if err != nil {
		return nil, err
	}
	base := b.baseURL(cfg)
	result := make([]api.Monitor, 0, len(cfg.Targets))
	for _, target := range cfg.Targets {
		item := api.Monitor{ID: target.ID, Name: target.Name, Collector: target.Collector, Watching: summarizeMatch(target), State: "unknown", Hosts: []api.MonitorHost{}, Rules: []api.MonitorRule{}, Beats: make([]string, beatCount), Kind: monitorKind(target)}
		for index, worst := range past.beats[target.ID] {
			item.Beats[index] = map[int]string{-1: "", 0: "down", 1: "unknown", 2: "up"}[worst]
		}
		if all := past.all[target.ID]; all > 0 {
			uptime := float64(past.up[target.ID]) * 100 / float64(all)
			item.Uptime = &uptime
		}
		if latency, ok := past.latency[target.ID]; ok {
			item.LatencyMS = &latency
		}
		if item.Name == "" {
			item.Name = target.ID
		}
		up, down := 0, 0
		for _, record := range states {
			if record.TargetID != target.ID {
				continue
			}
			var detail struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(record.Detail, &detail)
			if blueprint.ProbeOnly(target) {
				record.State, detail.Reason = "unknown", "等待第一次访问结果"
				if probe, ok := probes[[2]string{record.CollectorID, target.ID}]; ok {
					record.TS = probe.ts
					if probe.ok {
						record.State, detail.Reason = "up", "网址访问正常"
					} else {
						record.State, detail.Reason = "down", "访问失败："+probe.reason
					}
				}
			}
			item.Hosts = append(item.Hosts, api.MonitorHost{CollectorID: record.CollectorID, Hostname: names[record.CollectorID], State: record.State, Reason: detail.Reason, TS: record.TS})
			switch record.State {
			case "up":
				up++
			case "down":
				down++
			}
			if len(item.Hosts) == 1 {
				item.URL = base + "/#/hosts/" + url.PathEscape(record.CollectorID) + "/targets/" + url.PathEscape(target.ID)
			}
		}
		if down > 0 {
			item.State = "down"
		} else if up > 0 && up == len(item.Hosts) {
			item.State = "up"
		}
		for _, rule := range cfg.Rules {
			if rule.Target == target.ID {
				item.Rules = append(item.Rules, api.MonitorRule{ID: rule.ID, Type: rule.Type, Severity: rule.Severity})
			}
		}
		for _, alert := range firing {
			if alert.TargetID == target.ID {
				item.Firing++
			}
		}
		if slices.ContainsFunc(cfg.Dashboards, func(page config.Dashboard) bool { return page.ID == target.ID }) {
			item.Dashboard = target.ID
		}
		result = append(result, item)
	}
	return result, nil
}

func generatedRule(target, id string) bool {
	return slices.Contains([]string{target + "-process-down", target + "-service-stopped", target + "-unreachable", target + "-cert-expiry"}, id)
}

func (b *APIBackend) editRules(ctx context.Context, change func([]config.Rule) []config.Rule) error {
	_, err := b.options.Editor.Update(ctx, "rules.yaml", func(old []byte) ([]byte, error) {
		var file config.RulesFile
		if err := yaml.Unmarshal(old, &file); err != nil {
			return nil, fmt.Errorf("读取现有规则失败：%w", err)
		}
		file.Rules = change(file.Rules)
		return encodeYAML(file)
	})
	return err
}

func (b *APIBackend) editTargets(ctx context.Context, change func([]config.Target) []config.Target) error {
	_, err := b.options.Editor.Update(ctx, "targets.yaml", func(old []byte) ([]byte, error) {
		var file config.TargetsFile
		if err := yaml.Unmarshal(old, &file); err != nil {
			return nil, fmt.Errorf("读取现有目标失败：%w", err)
		}
		file.Targets = change(file.Targets)
		return encodeYAML(file)
	})
	return err
}

func (b *APIBackend) addMonitor(ctx context.Context, cfg *config.Config, r api.Request) (any, error) {
	var input blueprint.MonitorInput
	if err := api.DecodeBody(r.Body, &input); err != nil {
		return nil, err
	}
	if input.Collector != "" {
		host, err := b.pickCollector(ctx, input.Collector)
		if err != nil {
			return nil, err
		}
		input.Collector = host.ID
	}
	notify := input.Notify
	for _, id := range notify {
		if !slices.ContainsFunc(cfg.Notifiers, func(channel config.Notifier) bool { return channel.ID == id }) {
			return nil, invalidAPI("通知渠道 " + id + " 不存在；省略 notify 可使用全部已有渠道")
		}
	}
	result := api.MonitorResult{Next: []string{}}
	if len(notify) == 0 {
		for _, channel := range cfg.Notifiers {
			notify = append(notify, channel.ID)
		}
	}
	created := ""
	if len(notify) == 0 {
		created = "desktop"
		notify = []string{created}
	}
	plan, err := blueprint.Plan(input, notify)
	if err != nil {
		return nil, invalidAPI(err.Error())
	}
	id := plan.Target.ID
	existing := slices.IndexFunc(cfg.Targets, func(target config.Target) bool { return target.ID == id })
	result.Created = existing < 0
	if created != "" {
		// A monitor nobody hears about is worse than a local toast, which needs no credentials.
		if _, err := b.options.Editor.Update(ctx, "notifiers.yaml", func(old []byte) ([]byte, error) {
			file, err := config.ParseNotifiers(old)
			if err != nil {
				return nil, err
			}
			if !slices.ContainsFunc(file.Notifiers, func(channel config.Notifier) bool { return channel.ID == created }) {
				file.Notifiers = append(file.Notifiers, config.Notifier{ID: created, Type: "toast"})
			}
			return encodeYAML(file)
		}); err != nil {
			return nil, err
		}
		result.NotifierCreated = created
	}
	if existing >= 0 {
		// Rules generated for the old shape may not fit the new one, so they leave first.
		if err := b.editRules(ctx, func(items []config.Rule) []config.Rule {
			return slices.DeleteFunc(items, func(rule config.Rule) bool { return rule.Target == id && generatedRule(id, rule.ID) })
		}); err != nil {
			return nil, err
		}
	}
	if err := b.editTargets(ctx, func(items []config.Target) []config.Target {
		for index := range items {
			if items[index].ID == id {
				plan.Target.EventLog, plan.Target.Source = items[index].EventLog, items[index].Source
				if plan.Target.Tags == nil {
					plan.Target.Tags = items[index].Tags
				}
				items[index] = plan.Target
				return items
			}
		}
		return append(items, plan.Target)
	}); err != nil {
		return nil, err
	}
	if err := b.editRules(ctx, func(items []config.Rule) []config.Rule {
		items = slices.DeleteFunc(items, func(rule config.Rule) bool {
			return slices.ContainsFunc(plan.Rules, func(fresh config.Rule) bool { return fresh.ID == rule.ID })
		})
		return append(items, plan.Rules...)
	}); err != nil {
		return nil, fmt.Errorf("监控目标 %s 已保存，但告警规则保存失败：%w", id, err)
	}
	updated := b.options.Config.Snapshot()
	base := b.baseURL(updated)
	// Boards are per kind of content, not per monitor: the new monitor joins the services or websites page.
	board := plan.Template
	if input.Dashboard == nil || *input.Dashboard {
		if !slices.ContainsFunc(updated.DashboardStates, func(state config.DashboardState) bool { return state.ID == board }) {
			page, err := blueprint.Build(blueprint.TemplateInput{Template: board}, nil)
			if err == nil {
				_, err = b.saveDashboard(ctx, page, false)
			}
			if err != nil {
				result.Next = append(result.Next, "监控已生效，但分类面板没有生成："+err.Error())
			}
		}
	}
	if slices.ContainsFunc(b.options.Config.Snapshot().Dashboards, func(page config.Dashboard) bool { return page.ID == board }) {
		result.Dashboard, result.DashboardURL = board, base+"/#/boards/"+board
	}
	result.MonitorURL = base + "/#/monitors/" + url.PathEscape(id)
	list, err := b.monitors(ctx, b.options.Config.Snapshot())
	if err != nil {
		return nil, err
	}
	for _, item := range list {
		if item.ID == id {
			result.Monitor = item
		}
	}
	if result.Monitor.ID == "" {
		result.Monitor = api.Monitor{ID: id, Name: plan.Target.Name, Watching: summarizeMatch(plan.Target), State: "unknown", Hosts: []api.MonitorHost{}, Rules: []api.MonitorRule{}, Beats: make([]string, beatCount), Kind: monitorKind(plan.Target)}
	}
	result.Next = append(result.Next, "约十秒后调用 list_monitors 确认 "+id+" 的 state 变为 up；一直是 unknown 或 down 时读取 hosts[].reason。")
	if result.DashboardURL != "" {
		result.Next = append(result.Next, "把地址交给用户：监控详情 "+result.MonitorURL+"，分类面板 "+result.DashboardURL)
	}
	if result.NotifierCreated != "" {
		result.Next = append(result.Next, "还没有通知渠道，已自动启用本机 Windows 通知；要发到飞书，请用户在网页“通知 → 接入向导”里完成接入。")
	}
	return result, nil
}

func (b *APIBackend) removeMonitor(ctx context.Context, cfg *config.Config, id string) (any, error) {
	if !slices.ContainsFunc(cfg.Targets, func(target config.Target) bool { return target.ID == id }) {
		return nil, missingAPI("监控 " + id + " 不存在，先调用 list_monitors 核对 ID")
	}
	if err := b.editRules(ctx, func(items []config.Rule) []config.Rule {
		return slices.DeleteFunc(items, func(rule config.Rule) bool { return rule.Target == id })
	}); err != nil {
		return nil, err
	}
	if err := b.editTargets(ctx, func(items []config.Target) []config.Target {
		return slices.DeleteFunc(items, func(target config.Target) bool { return target.ID == id })
	}); err != nil {
		return nil, err
	}
	message := "监控 " + id + " 及其告警规则已删除"
	// Older versions made one page per monitor; a category page narrowed to this monitor would only show empty cards now.
	removed := 0
	for _, page := range []string{id, "services-" + id, "websites-" + id} {
		if slices.ContainsFunc(cfg.DashboardStates, func(state config.DashboardState) bool { return state.ID == page }) {
			if _, err := b.options.Editor.DeleteDashboard(ctx, page); err == nil {
				removed++
			}
		}
	}
	if removed > 0 {
		message += fmt.Sprintf("，只看它的 %d 页面板也已删除", removed)
	}
	return api.Confirmation{OK: true, Message: message}, nil
}

// saveDashboard validates a page, proves every card query runs, and only then writes the file.
func (b *APIBackend) saveDashboard(ctx context.Context, page config.Dashboard, strict bool) (api.DashboardResult, error) {
	var result api.DashboardResult
	if page.Refresh == "" {
		page.Refresh = "10s"
	}
	if issues := config.CheckDashboard(page); len(issues) > 0 {
		return result, &api.Fault{Status: 422, Code: "invalid_dashboard", Message: issues.Error(), Cause: issues}
	}
	span := page.DefaultRange
	if span == "" {
		span = "1h"
	}
	var problems []string
	for _, card := range page.Panels {
		data, err := b.runSQL(ctx, card.Query, config.QueryParams{}, span)
		if err == nil {
			err = store.ValidatePanelResult(card.Type, data.QueryResult)
		}
		if err != nil {
			problems = append(problems, "卡片 "+card.ID+"（"+card.Type+"）："+err.Error())
		}
	}
	if len(problems) > 0 && strict {
		return result, &api.Fault{Status: 422, Code: "panel_query_failed", Message: "面板没有保存，以下卡片的查询需要修改：" + strings.Join(problems, "；") + "。先用 get_schema 核对表和列，再用 query_sql 试跑。"}
	}
	if len(problems) > 0 {
		return result, fmt.Errorf("%s", strings.Join(problems, "；"))
	}
	data, err := encodeYAML(page)
	if err != nil {
		return result, err
	}
	before := b.options.Config.Snapshot()
	result.Created = !slices.ContainsFunc(before.DashboardStates, func(state config.DashboardState) bool { return state.ID == page.ID })
	updated, err := b.options.Editor.SaveDashboard(ctx, page.ID, data)
	if err != nil {
		return result, err
	}
	result.ID, result.Title, result.Panels, result.URL = page.ID, page.Title, len(page.Panels), b.baseURL(updated)+"/#/boards/"+page.ID
	return result, nil
}

func (b *APIBackend) agentOperation(ctx context.Context, cfg *config.Config, op api.Operation, r api.Request) (any, error) {
	b.noteAgent(ctx, r)
	switch op {
	case api.GetGuide:
		return b.guide(ctx, cfg)
	case api.Explore:
		return b.explore(ctx, cfg, r)
	case api.ListMonitors:
		return b.monitors(ctx, cfg)
	case api.AddMonitor:
		return b.addMonitor(ctx, cfg, r)
	case api.RemoveMonitor:
		return b.removeMonitor(ctx, cfg, r.Params["id"])
	case api.ListDashboardTemplates:
		return blueprint.Templates(), nil
	case api.CreateDashboard:
		var input blueprint.TemplateInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		page, err := blueprint.Build(input, func(id string) bool {
			return slices.ContainsFunc(cfg.Targets, func(target config.Target) bool { return target.ID == id })
		})
		if err != nil {
			return nil, invalidAPI(err.Error())
		}
		return b.saveDashboard(ctx, page, true)
	case api.PutDashboard:
		var page config.Dashboard
		if err := api.DecodeBody(r.Body, &page); err != nil {
			return nil, err
		}
		if page.ID == "" {
			page.ID = r.Params["id"]
		}
		if page.ID != r.Params["id"] {
			return nil, invalidAPI("面板定义里的 id 必须与路径中的 id 一致")
		}
		return b.saveDashboard(ctx, page, true)
	case api.DeleteDashboard:
		id := r.Params["id"]
		if !slices.ContainsFunc(cfg.DashboardStates, func(state config.DashboardState) bool { return state.ID == id }) {
			return nil, missingAPI("面板 " + id + " 不存在，先调用 list_dashboards 核对 ID")
		}
		if _, err := b.options.Editor.DeleteDashboard(ctx, id); err != nil {
			return nil, err
		}
		return api.Confirmation{OK: true, Message: "面板 " + id + " 已删除"}, nil
	case api.GetAssistant:
		return b.assistantStatus(ctx, cfg), nil
	case api.ConfigureAssistant:
		return b.configureAssistant(ctx, cfg, r)
	}
	return nil, fmt.Errorf("HTTP 业务操作 %s 未实现", op)
}

func absolutePath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}

func executablePath() string {
	if path, err := os.Executable(); err == nil {
		return path
	}
	return "lgmon"
}
