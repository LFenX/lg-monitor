package app

import (
	"bytes"
	"cmp"
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
	mu                sync.Mutex
	seenAt            int64
	seenName          string
	savedAt           int64
	history           *monitorHistory
	historyAt         time.Time
	historyOf         *config.Config
	historyHadFailure bool
	snapshots         map[string]processSnapshot
	// One page refresh asks for the board list, the board and the guide at once; they share this.
	listed   []api.Monitor
	listedAt time.Time
	listedOf *config.Config
}

// monitorCacheTTL is short enough that a state change shows up on the next page refresh.
const monitorCacheTTL = 2 * time.Second

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
	state := api.GuideState{Collectors: len(hosts), Monitors: len(cfg.Targets), FiringAlerts: len(firing), Dashboards: len(cfg.Dashboards), Boards: len(cfg.Boards), Notifiers: len(cfg.Notifiers)}
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
		{ID: "agent", Title: "让 Agent 接入", Done: state.AgentSeenAt > 0 || principal.Scope.Allows(api.ScopeConfig) && state.Monitors > 0, How: "把网页完整提示词交给 Agent，由它在监控器电脑执行：" + connect + " --elevate --json，需要管理员时由本人确认 Windows 授权。手动接入仍可选；令牌只在内存中使用，不回显到聊天或日志。"},
		{ID: "board", Title: "建一个面板", Done: len(cfg.Boards) > 0, Tool: "create_board", How: "面板是一组服务，一般一个就够。还没有目标面板时，Agent 直接调用 create_board，使用本人指定的名称和简介，再把返回的 ID 传给 add_monitor，不要求本人先手动建。未指定名字时也可由 add_monitor 自动建“我的服务”。"},
		{ID: "monitor", Title: "添加第一个服务", Done: state.Monitors > 0, Tool: "add_monitor", How: "先用 explore 看这台电脑上在运行什么，再用 add_monitor 传入 name 加上 port、process、service、url 之一。一次调用会建好目标、告警规则和通知，并放进面板。附属的部分（Tunnel、网关、数据库）再调用 add_monitor 并传 parent 挂在主服务下面。"},
		{ID: "notify", Title: "把告警发到手机", Done: feishu, How: "默认只有本机 Windows 通知。需要飞书时，把网页“通知 → 接入飞书”的完整提示词交给 Agent，由它检查、找群、测试和保存。本人只配合创建机器人、在本机安全填写凭据、权限发布、加群和确认真实收件；Agent 不接触应用密钥。"},
	}
	return api.Guide{
		Product: "lgmon：监控 Windows 电脑上的服务（端口、进程、Windows 服务、网址），按面板组织、主服务下可挂子服务，异常时告警。", Version: b.options.Version,
		Scope: principal.Scope, CanConfigure: principal.Scope.Allows(api.ScopeConfig),
		Install: api.GuideInstall{Executable: executable, Home: home, Personal: b.options.Personal, URL: base},
		State:   state, Collectors: hosts, Steps: steps,
		Workflow: []string{
			"1. start_here：了解现状和下一步（就是这次调用）。",
			"2. list_boards：看有哪些面板；用户说“放进某某面板”时用它找到 board ID，没有面板也没关系。",
			"3. explore：看这台电脑上有哪些端口、Windows 服务、进程可以监控；q 可按名字搜索。",
			"4. add_monitor：一次调用加好一个服务；用 explore 返回的 suggest 作参数最稳妥，需要时带上 board。",
			"5. 附属服务（给网站做穿透的 Tunnel、AI 网关、它依赖的数据库）再调用 add_monitor 并传 parent=主服务 ID，它们会显示在主服务下面，异常时主服务整体算异常。",
			"6. get_board 或 list_monitors：确认 state，unknown 表示还没采到第一轮数据，等十秒再看。",
			"7. 把返回的 boardUrl 和 monitorUrl 交给用户，在浏览器里查看。用户另外要图表时才用 put_dashboard 做自定义视图。",
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
	probeOnly := blueprint.ProbeOnly(target) || target.Task != ""
	if target.Task != "" {
		parts = append(parts, "计划任务 "+target.Task)
	}
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
	slots   map[string][]api.StatusSlot
	up, all map[string]int64
	latency map[string]float64
}

func (b *APIBackend) history(ctx context.Context, cfg *config.Config, hasFailure bool) (monitorHistory, error) {
	now := b.options.Now()
	b.agent.mu.Lock()
	cached, at := b.agent.history, b.agent.historyAt
	cacheValid := b.agent.historyOf == cfg && !hasFailure && !b.agent.historyHadFailure
	b.agent.mu.Unlock()
	if cached != nil && cacheValid && now.Sub(at) < historyTTL {
		return *cached, nil
	}
	result := monitorHistory{beats: map[string][]int{}, slots: map[string][]api.StatusSlot{}, up: map[string]int64{}, all: map[string]int64{}, latency: map[string]float64{}}
	from := now.Add(-beatCount * beatSpan).UnixMilli()
	span := beatSpan.Milliseconds()
	// Each target's health comes from one table: probes for sites, the task record for
	// scheduled tasks, and the collector's own status for everything else.
	source := map[string]string{}
	for _, target := range cfg.Targets {
		result.slots[target.ID] = make([]api.StatusSlot, beatCount)
		for index := range result.slots[target.ID] {
			result.slots[target.ID][index].From = from + int64(index)*span
		}
		switch {
		case target.Task != "":
			source[target.ID] = "task"
		case blueprint.ProbeOnly(target):
			source[target.ID] = "probe"
		}
	}
	add := func(target string, bucket, worst, up, all int64) {
		bucket = min(bucket, beatCount-1)
		if bucket < 0 {
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
		slot := &result.slots[target][bucket]
		slot.State = map[int64]string{0: "down", 1: "unknown", 2: "up"}[worst]
		slot.Uptime = percent(float64(up), float64(all))
	}
	read := func(query string, kind string) error {
		rows, err := b.options.SQL.Query(ctx, query)
		if err != nil {
			return err
		}
		for _, row := range rows.Rows {
			target, _ := row[0].(string)
			if source[target] != kind {
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
	for _, target := range cfg.Targets {
		normalized := confirmedHealthSource(target, from-(config.FailureConfirmation+healthGap(target)).Milliseconds(), now.UnixMilli())
		query := `SELECT target_id, MIN(CAST((ts - ` + fromText + `) / ` + spanText + ` AS INTEGER),` + strconv.Itoa(beatCount-1) + `) AS b, ` + stateWorstSQL + `, SUM(CASE WHEN state='up' THEN 1 ELSE 0 END), SUM(CASE WHEN state IN ('up','down') THEN 1 ELSE 0 END) FROM (` + normalized + `) WHERE ts>=` + fromText + ` GROUP BY 1,2`
		if err := read(query, source[target.ID]); err != nil {
			return result, err
		}
		detail := api.MonitorDetail{Slots: result.slots[target.ID]}
		if err := b.monitorSegments(ctx, &detail, target, healthSource(target), from, now.UnixMilli()); err != nil {
			return result, err
		}
		result.slots[target.ID] = detail.Slots
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
	b.agent.historyOf, b.agent.historyHadFailure = cfg, hasFailure
	b.agent.mu.Unlock()
	return result, nil
}

func monitorKind(target config.Target) string {
	switch {
	case target.Task != "":
		return "task"
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
	now := b.options.Now()
	b.agent.mu.Lock()
	if b.agent.listedOf == cfg && b.agent.listed != nil && now.Sub(b.agent.listedAt) >= 0 && now.Sub(b.agent.listedAt) < monitorCacheTTL {
		cached := slices.Clone(b.agent.listed)
		b.agent.mu.Unlock()
		return cached, nil
	}
	b.agent.mu.Unlock()
	result, err := b.listMonitors(ctx, cfg)
	if err == nil {
		b.agent.mu.Lock()
		b.agent.listed, b.agent.listedAt, b.agent.listedOf = slices.Clone(result), now, cfg
		b.agent.mu.Unlock()
	}
	return result, err
}

func (b *APIBackend) listMonitors(ctx context.Context, cfg *config.Config) ([]api.Monitor, error) {
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
	tasks, err := b.taskStates(ctx, cfg)
	if err != nil {
		return nil, err
	}
	hasFailure := false
	activeTargets := targetIndex(cfg)
	for _, state := range states {
		if _, active := activeTargets[state.TargetID]; active && state.State == "down" {
			hasFailure = true
		}
	}
	for _, probe := range probes {
		if !probe.ok {
			hasFailure = true
		}
	}
	for _, task := range tasks {
		if task.state == "down" {
			hasFailure = true
		}
	}
	past, err := b.history(ctx, cfg, hasFailure)
	if err != nil {
		return nil, err
	}
	base := b.baseURL(cfg)
	result := make([]api.Monitor, 0, len(cfg.Targets))
	for _, target := range cfg.Targets {
		item := api.Monitor{ID: target.ID, Name: target.Name, Collector: target.Collector, Watching: summarizeMatch(target), State: "unknown", Hosts: []api.MonitorHost{}, Rules: []api.MonitorRule{}, Beats: make([]string, beatCount), Kind: monitorKind(target), Children: []string{}, Boards: []string{}}
		item.BeatSlots = past.slots[target.ID]
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
			if target.Task != "" {
				record.State, detail.Reason, record.TS = "unknown", "等待第一次读取计划任务（约一分钟）", 0
				if task, ok := tasks[[2]string{record.CollectorID, target.ID}]; ok {
					record.State, detail.Reason, record.TS = task.state, task.reason, task.ts
				}
			} else if blueprint.ProbeOnly(target) {
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
	decorate(cfg, result, base)
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
	// A scheduled task lives on one computer, so it is pinned to a collector even when only one exists.
	if input.Collector != "" || strings.TrimSpace(input.Task) != "" {
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
	board := strings.TrimSpace(input.Board)
	if _, ok := cfg.FindBoard(board); board != "" && !ok {
		return nil, invalidAPI("面板 " + board + " 不存在；先调用 list_boards 查看，或省略 board 放进第一个面板")
	}
	if existing >= 0 {
		plan.Target.Parent = cfg.Targets[existing].Parent
	}
	if input.Parent != nil {
		plan.Target.Parent = strings.TrimSpace(*input.Parent)
	}
	if err := checkParent(cfg, id, plan.Target.Parent); err != nil {
		return nil, err
	}
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
	placed, boardCreated, err := b.placeMonitor(ctx, id, board)
	if err != nil {
		result.Next = append(result.Next, "监控已生效，但没能放进面板："+err.Error())
	}
	updated := b.options.Config.Snapshot()
	base := b.baseURL(updated)
	if _, ok := updated.FindBoard(placed); ok {
		result.Board, result.BoardCreated, result.BoardURL = placed, boardCreated, base+"/#/boards/"+url.PathEscape(placed)
	}
	result.MonitorURL = base + "/#/monitors/" + url.PathEscape(id)
	list, err := b.monitors(ctx, updated)
	if err != nil {
		return nil, err
	}
	for _, item := range list {
		if item.ID == id {
			result.Monitor = item
		}
	}
	if result.Monitor.ID == "" {
		result.Monitor = api.Monitor{ID: id, Name: plan.Target.Name, Watching: summarizeMatch(plan.Target), State: "unknown", Overall: "unknown", Hosts: []api.MonitorHost{}, Rules: []api.MonitorRule{}, Beats: make([]string, beatCount), Kind: monitorKind(plan.Target), Parent: plan.Target.Parent, Children: []string{}, Boards: []string{}, MonitorURL: result.MonitorURL}
	}
	wait := "约十秒"
	if plan.Target.Task != "" {
		wait = "约一分钟"
	}
	result.Next = append(result.Next, wait+"后调用 list_monitors 确认 "+id+" 的 state 变为 up；一直是 unknown 或 down 时读取 hosts[].reason。")
	if plan.Target.Parent == "" {
		result.Next = append(result.Next, "这个服务如果有附属的部分（Tunnel、网关、数据库、计划任务等），再调用 add_monitor 并传 parent: \""+id+"\"，它们会挂在这个服务下面。")
	}
	if result.BoardURL != "" {
		result.Next = append(result.Next, "把地址交给用户：面板 "+result.BoardURL+"，监控详情 "+result.MonitorURL)
	}
	if result.NotifierCreated != "" {
		result.Next = append(result.Next, "还没有通知渠道，已自动启用本机 Windows 通知；要发到飞书，请用户在网页“设置 → 通知”里完成接入。")
	}
	return result, nil
}

// checkParent rejects a parent that would hide the monitor in a loop or nest it too deep.
func checkParent(cfg *config.Config, id, parent string) error {
	if parent == "" {
		return nil
	}
	if parent == id {
		return invalidAPI("parent 不能是这个监控自己")
	}
	targets := targetIndex(cfg)
	if _, ok := targets[parent]; !ok {
		return invalidAPI("主服务 " + parent + " 不存在；先调用 list_monitors 核对 ID，或先添加主服务")
	}
	above := ancestors(targets, parent)
	if slices.Contains(above, id) {
		return invalidAPI("不能把 " + id + " 挂到它自己的子服务 " + parent + " 下面")
	}
	height := 0
	children := config.Children(cfg.Targets)
	var measure func(string, int)
	measure = func(current string, depth int) {
		height = max(height, depth)
		for _, child := range children[current] {
			if child != id && depth < config.MaxMonitorDepth {
				measure(child, depth+1)
			}
		}
	}
	measure(id, 1)
	if len(above)+1+height > config.MaxMonitorDepth {
		return invalidAPI("子服务最多三层（主服务、子服务、子服务的子服务）；请把 " + id + " 挂到更上层的服务下面")
	}
	return nil
}

func (b *APIBackend) removeMonitor(ctx context.Context, cfg *config.Config, id string, withChildren bool) (any, error) {
	if !slices.ContainsFunc(cfg.Targets, func(target config.Target) bool { return target.ID == id }) {
		return nil, missingAPI("监控 " + id + " 不存在，先调用 list_monitors 核对 ID")
	}
	below := descendants(config.Children(cfg.Targets), id)
	if len(below) > 0 && !withChildren {
		targets := targetIndex(cfg)
		names := make([]string, 0, len(below))
		for _, child := range below {
			names = append(names, cmp.Or(targets[child].Name, child))
		}
		return nil, &api.Fault{Status: 409, Code: "has_children", Message: fmt.Sprintf("监控 %s 下面还有 %d 个子服务（%s）。确认整组删除时传 children=true；只想保留子服务时，先用 add_monitor 把它们的 parent 改成空字符串", id, len(below), strings.Join(names, "、"))}
	}
	group := append([]string{id}, below...)
	gone := func(target string) bool { return slices.Contains(group, target) }
	if slices.ContainsFunc(cfg.Boards, func(board config.Board) bool { return slices.ContainsFunc(board.Monitors, gone) }) {
		if err := b.editBoards(ctx, func(items []config.Board) ([]config.Board, error) {
			for index := range items {
				items[index].Monitors = slices.DeleteFunc(items[index].Monitors, gone)
			}
			return items, nil
		}); err != nil {
			return nil, err
		}
	}
	if err := b.editRules(ctx, func(items []config.Rule) []config.Rule {
		return slices.DeleteFunc(items, func(rule config.Rule) bool { return gone(rule.Target) })
	}); err != nil {
		return nil, err
	}
	if err := b.editTargets(ctx, func(items []config.Target) []config.Target {
		return slices.DeleteFunc(items, func(target config.Target) bool { return gone(target.ID) })
	}); err != nil {
		return nil, err
	}
	message := "监控 " + id + " 及其告警规则已删除"
	if len(below) > 0 {
		message = fmt.Sprintf("监控 %s 和它的 %d 个子服务及告警规则已删除", id, len(below))
	}
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
		return b.removeMonitor(ctx, cfg, r.Params["id"], r.Query.Get("children") == "true")
	case api.ListBoards, api.GetBoard, api.CreateBoard, api.UpdateBoard, api.DeleteBoard:
		return b.boardOperation(ctx, cfg, op, r)
	case api.GetMonitor:
		return b.monitorDetail(ctx, cfg, r.Params["id"])
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
