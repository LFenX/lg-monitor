package app

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	sessions          map[string]*agentSession
	monitorsFlight    *monitorFlight
	historyScan       sync.Mutex
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

type monitorFlight struct {
	cfg    *config.Config
	done   chan struct{}
	result []api.Monitor
	err    error
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
	name := b.touchAgent(r, principal.Name, now, true).Name
	b.agent.mu.Unlock()
	b.noteAgentSeen(now, name)
}

// noteAgentSeen remembers the latest agent contact for the guide; it is saved at most once a minute.
func (b *APIBackend) noteAgentSeen(now int64, name string) {
	b.agent.mu.Lock()
	b.agent.seenAt, b.agent.seenName = now, name
	persist := now-b.agent.savedAt >= time.Minute.Milliseconds()
	if persist {
		b.agent.savedAt = now
	}
	b.agent.mu.Unlock()
	if path := b.agentSeenPath(); persist && path != "" {
		data, _ := json.Marshal(agentSeen{At: now, Name: name})
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
		State:   state, Collectors: hosts, Steps: steps, Modules: api.MCPModules,
		Workflow: []string{
			"1. start_here：了解现状和下一步（就是这次调用）。",
			"2. 问用户这次想做什么：要监控哪些服务、它们有没有附属的部分、告警要不要发到飞书。不要让用户自己去查端口、进程名或群 ID，这些由你用工具找。",
			"3. list_boards：看有哪些面板；用户说“放进某某面板”时用它找到 board ID，没有面板也没关系。",
			"4. explore：看这台电脑上有哪些端口、Windows 服务、进程、计划任务可以监控；q 可按名字搜索。找到后跟用户确认是不是这个。",
			"5. add_monitor：一次调用加好一个服务；用 explore 返回的 suggest 作参数最稳妥，需要时带上 board。顺手写 description（一两句：做什么、谁在用），看得出是什么时写 role（数据库、隧道、网关……决定图标）。",
			"6. 附属服务（给网站做穿透的 Tunnel、AI 网关、它依赖的数据库）再调用 add_monitor 并传 parent=主服务 ID，它们会显示在主服务下面，每个服务只显示自己的探测状态。服务之间的使用关系（调用接口、读写数据库、经由网关、由计划任务触发）用 depends 写在使用的一方，可以跨面板；它画在依赖关系图里，不影响告警。已有的服务用 edit_monitor 补说明、角色和依赖。",
			"7. 需要更长的说明时（部署在哪、怎么启动、依赖谁、出问题怎么查），用 put_monitor_doc 写 Markdown 文档，先 get_monitor_doc 读原文；[[服务ID]] 会变成服务链接，deps 代码块会画依赖关系图。不写密钥。",
			"8. get_board 或 list_monitors：确认 state，unknown 表示还没采到第一轮数据，等十秒再看。",
			"9. 把返回的 boardUrl 和 monitorUrl 交给用户，在浏览器里查看。其他事情（告警、飞书、图表、内网共享、助手）见 modules，每个模块写了什么时候用哪些工具。",
		},
		Rules: []string{
			"只监控用户点名或确认过的对象；不停止、重启或修改任何已有的进程和服务。",
			"当前权限是 " + string(principal.Scope) + "；写操作需要 config。权限不足时把原因和 install.executable 的 agent connect 命令告诉用户，不要绕过。",
			"不主动索取密钥或令牌；飞书凭证由用户自己在网页或命令行里填写；波西米亚的密钥只在用户主动提供时用 configure_assistant 保存，之后不复述。",
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
	// While something is down the bars refresh sooner, but a failure no longer forces a full scan per request.
	failingHistoryTTL = 10 * time.Second
	// apiReaders is how many reads the API runs at once on each read-only connection pool.
	apiReaders = 4
)

type monitorHistory struct {
	beats   map[string][]int // worst state per bucket: 0 down, 1 unknown, 2 up, -1 no data
	slots   map[string][]api.StatusSlot
	up, all map[string]int64
	latency map[string]float64
}

func (b *APIBackend) history(ctx context.Context, cfg *config.Config, hasFailure bool) (monitorHistory, error) {
	// Several views can miss the cache together. One scan fills it for all of them.
	b.agent.historyScan.Lock()
	defer b.agent.historyScan.Unlock()
	if err := ctx.Err(); err != nil {
		return monitorHistory{}, err
	}
	now := b.options.Now()
	b.agent.mu.Lock()
	cached, at := b.agent.history, b.agent.historyAt
	ttl := historyTTL
	if hasFailure || b.agent.historyHadFailure {
		ttl = failingHistoryTTL
	}
	cacheValid := b.agent.historyOf.SameRevision(cfg) && hasFailure == b.agent.historyHadFailure && now.Sub(at) >= 0 && now.Sub(at) < ttl
	b.agent.mu.Unlock()
	if cached != nil && cacheValid {
		return *cached, nil
	}
	result := monitorHistory{beats: map[string][]int{}, slots: map[string][]api.StatusSlot{}, up: map[string]int64{}, all: map[string]int64{}, latency: map[string]float64{}}
	from := now.Add(-beatCount * beatSpan).UnixMilli()
	span := beatSpan.Milliseconds()
	if err := b.readMonitorHistory(ctx, cfg.Targets, from, now.UnixMilli(), span, &result); err != nil {
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
	b.agent.historyOf, b.agent.historyHadFailure = cfg, hasFailure
	b.agent.mu.Unlock()
	return result, nil
}

type monitorBeat struct {
	worst    int
	up, all  int64
	observed bool
}

func (b *monitorBeat) add(state string, count int64) {
	worst := 1
	switch state {
	case "down":
		worst = 0
		b.all += count
	case "up":
		worst = 2
		b.up += count
		b.all += count
	}
	if !b.observed || worst < b.worst {
		b.worst = worst
	}
	b.observed = true
}

type monitorSampleRun struct {
	state                   string
	first, last, firstShown int64
	shown                   bool
	down                    [beatCount]int64
}

type monitorSeries struct {
	collectors map[string]*monitorSampleRun
	gap        int64
	count      bool
	show       bool
	beats      [beatCount]monitorBeat
	runs       []monitorRun
}

func (s *monitorSeries) finish(collector string, run *monitorSampleRun, until int64) {
	end := min(until, run.last+s.gap)
	if s.count && run.state == "down" {
		state := run.state
		if end-run.first < config.FailureConfirmation.Milliseconds() {
			state = "up"
		}
		for index, count := range run.down {
			if count > 0 {
				s.beats[index].add(state, count)
			}
		}
	}
	if s.show && run.shown {
		// The chart begins at its first visible observation; availability also uses the lookback.
		s.runs = append(s.runs, monitorRun{collector: collector, from: run.firstShown, to: end, state: run.state})
	}
}

func (b *APIBackend) readMonitorHistory(ctx context.Context, targets []config.Target, from, until, span int64, result *monitorHistory) error {
	bySource := map[string]map[string]*monitorSeries{}
	add := func(source string, target config.Target) *monitorSeries {
		items := bySource[source]
		if items == nil {
			items = map[string]*monitorSeries{}
			bySource[source] = items
		}
		series := items[target.ID]
		if series == nil {
			series = &monitorSeries{gap: healthGap(target).Milliseconds(), collectors: map[string]*monitorSampleRun{}}
			items[target.ID] = series
		}
		return series
	}
	for _, target := range targets {
		if target.Job != nil {
			continue
		}
		slots := make([]api.StatusSlot, beatCount)
		for index := range slots {
			slots[index].From = from + int64(index)*span
		}
		result.slots[target.ID] = slots
		source := healthSource(target)
		add(source, target).show = true
		if blueprint.ProbeOnly(target) {
			source = "probe_results"
		}
		add(source, target).count = true
	}
	for source, items := range bySource {
		ids := make([]string, 0, len(items))
		lookback := from
		for id, series := range items {
			ids = append(ids, id)
			if series.count {
				lookback = min(lookback, from-config.FailureConfirmation.Milliseconds()-series.gap)
			}
		}
		err := b.options.SQL.VisitMonitorSamples(ctx, source, ids, lookback, until, func(id, collector string, at int64, state string) {
			series := items[id]
			if at < from-config.FailureConfirmation.Milliseconds()-series.gap {
				return
			}
			run := series.collectors[collector]
			if run == nil {
				run = &monitorSampleRun{state: state, first: at}
				series.collectors[collector] = run
			} else if state != run.state || at-run.last > series.gap {
				series.finish(collector, run, at)
				*run = monitorSampleRun{state: state, first: at}
			}
			run.last = at
			if at < from {
				return
			}
			if !run.shown {
				run.firstShown, run.shown = at, true
			}
			if series.count {
				bucket := min((at-from)/span, beatCount-1)
				if state == "down" {
					run.down[bucket]++
				} else {
					series.beats[bucket].add(state, 1)
				}
			}
		})
		if err != nil {
			return err
		}
		for id, series := range items {
			for collector, run := range series.collectors {
				series.finish(collector, run, until)
			}
			slots := result.slots[id]
			if series.count {
				beats := make([]int, beatCount)
				for index, beat := range series.beats {
					beats[index] = -1
					if !beat.observed {
						continue
					}
					beats[index] = beat.worst
					slots[index].State = worstName[int64(beat.worst)]
					slots[index].Uptime = percent(float64(beat.up), float64(beat.all))
					result.up[id] += beat.up
					result.all[id] += beat.all
				}
				result.beats[id] = beats
			}
			if len(series.runs) > store.QueryLimit {
				slices.SortFunc(series.runs, func(a, b monitorRun) int {
					if order := cmp.Compare(b.from, a.from); order != 0 {
						return order
					}
					return cmp.Compare(a.collector, b.collector)
				})
				series.runs = series.runs[:store.QueryLimit]
			}
			attachMonitorRuns(&api.MonitorDetail{Slots: slots}, series.runs, until)
		}
	}
	return nil
}

func monitorKind(target config.Target) string {
	switch {
	case target.Job != nil:
		return "job"
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
	probeRange := store.MonitorSampleRangeSQL("probe_results", `target_id IN (`+strings.Join(ids, ",")+`)`)
	rows, err := b.options.SQL.Query(ctx, `SELECT p.collector_id, p.target_id, p.ok, COALESCE(NULLIF(p.error, ''), NULLIF(p.message, ''), '') AS reason, p.ts FROM (`+probeRange+`) p WHERE p.ts = (SELECT max(n.ts) FROM probe_results n WHERE n.collector_id = p.collector_id AND n.target_id = p.target_id AND n.probe = p.probe)`)
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
	for {
		b.agent.mu.Lock()
		if b.agent.listedOf == cfg && b.agent.listed != nil && now.Sub(b.agent.listedAt) >= 0 && now.Sub(b.agent.listedAt) < monitorCacheTTL {
			cached := slices.Clone(b.agent.listed)
			b.agent.mu.Unlock()
			return cached, nil
		}
		// Coalesce concurrent in-flight misses (/boards, /monitors, /boards/{id} arrive together on page load)
		// without serving stale state to later sequential requests.
		if flight := b.agent.monitorsFlight; flight != nil && flight.cfg.SameRevision(cfg) {
			b.agent.mu.Unlock()
			select {
			case <-flight.done:
				if flight.err == nil {
					return slices.Clone(flight.result), nil
				}
				if errors.Is(flight.err, context.Canceled) && ctx.Err() == nil {
					continue
				}
				return nil, flight.err
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if err := ctx.Err(); err != nil {
			b.agent.mu.Unlock()
			return nil, err
		}
		flight := &monitorFlight{cfg: cfg, done: make(chan struct{})}
		b.agent.monitorsFlight = flight
		b.agent.mu.Unlock()

		result, err := b.listMonitors(ctx, cfg)
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			// The query budget protects the database; right after start a moment of contention can hit it. Say what it is.
			err = &api.Fault{Status: 503, Code: "monitor_busy", Message: "监控器正忙着整理数据（刚启动时常见），几秒后会自动刷新", Cause: err}
		}
		flight.result, flight.err = result, err
		close(flight.done)

		b.agent.mu.Lock()
		if b.agent.monitorsFlight == flight {
			b.agent.monitorsFlight = nil
		}
		if err == nil {
			b.agent.listed, b.agent.listedAt, b.agent.listedOf = slices.Clone(result), now, cfg
		}
		b.agent.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return result, nil
	}
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
	depends, dependents := dependencyViews(cfg)
	result := make([]api.Monitor, 0, len(cfg.Targets))
	for _, target := range cfg.Targets {
		item := api.Monitor{ID: target.ID, Name: target.Name, Collector: target.Collector, Watching: summarizeMatch(target), State: "unknown", Hosts: []api.MonitorHost{}, Rules: []api.MonitorRule{}, Beats: make([]string, beatCount), Kind: monitorKind(target), Children: []string{}, Boards: []string{}}
		item.Role, item.RoleGuessed = serviceRole(target)
		item.Entry, item.EntryGuessed = serviceEntry(target)
		item.Description, item.Depends, item.Dependents = target.Description, depends[target.ID], dependents[target.ID]
		item.HasDoc = cfg.Docs[target.ID] != ""
		if target.Job != nil {
			// A job's state is its latest day's result; it has no host samples.
			days, err := b.jobDays(ctx, target, 2)
			if err != nil {
				return nil, err
			}
			item.Watching = []string{"批处理任务", scheduleText(target.Job.Schedule)}
			item.Job, item.State = jobBrief(days)
			if item.State == "down" && item.Job != nil {
				item.Issue = item.Job.Summary
			}
			if info, err := b.jobInfo(ctx, target); err != nil {
				return nil, err
			} else if item.Issue == "" && info.File != nil && info.File.State == "error" {
				item.Issue = "事件文件读不了：" + info.File.Problem
			}
			result = append(result, item)
			continue
		}
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
		old := cfg.Targets[existing]
		plan.Target.Parent, plan.Target.Role, plan.Target.Description, plan.Target.Depends, plan.Target.Entry = old.Parent, old.Role, old.Description, old.Depends, old.Entry
	}
	if input.Parent != nil {
		plan.Target.Parent = strings.TrimSpace(*input.Parent)
	}
	if err := applyRelations(cfg, &plan.Target, input.Description, input.Role, input.Depends, input.Entry); err != nil {
		return nil, err
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
		result.Monitor = api.Monitor{ID: id, Name: plan.Target.Name, Watching: summarizeMatch(plan.Target), State: "unknown", Overall: "unknown", Hosts: []api.MonitorHost{}, Rules: []api.MonitorRule{}, Beats: make([]string, beatCount), Kind: monitorKind(plan.Target), Parent: plan.Target.Parent, Children: []string{}, Boards: []string{}, MonitorURL: result.MonitorURL, Description: plan.Target.Description}
		result.Monitor.Role, result.Monitor.RoleGuessed = serviceRole(plan.Target)
		result.Monitor.Entry, result.Monitor.EntryGuessed = serviceEntry(plan.Target)
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
// editMonitor renames or re-parents a monitor; the target's checks and rules stay as they are.
func (b *APIBackend) editMonitor(ctx context.Context, cfg *config.Config, r api.Request) (any, error) {
	id := r.Params["id"]
	var input api.MonitorEdit
	if err := api.DecodeBody(r.Body, &input); err != nil {
		return nil, err
	}
	index := slices.IndexFunc(cfg.Targets, func(target config.Target) bool { return target.ID == id })
	if index < 0 {
		return nil, missingAPI("监控 " + id + " 不存在")
	}
	name := cfg.Targets[index].Name
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
		if name == "" || utf8.RuneCountInString(name) > 80 {
			return nil, invalidAPI("名字需要 1～80 个字")
		}
	}
	parent := cfg.Targets[index].Parent
	if input.Parent != nil {
		parent = strings.TrimSpace(*input.Parent)
		if err := checkParent(cfg, id, parent); err != nil {
			return nil, err
		}
	}
	described := cfg.Targets[index]
	if err := applyRelations(cfg, &described, input.Description, input.Role, input.Depends, input.Entry); err != nil {
		return nil, err
	}
	if err := b.editTargets(ctx, func(items []config.Target) []config.Target {
		for i := range items {
			if items[i].ID == id {
				items[i].Name, items[i].Parent = name, parent
				items[i].Role, items[i].Description, items[i].Depends, items[i].Entry = described.Role, described.Description, described.Depends, described.Entry
			}
		}
		return items
	}); err != nil {
		return nil, err
	}
	updated := b.options.Config.Snapshot()
	monitors, err := b.monitors(ctx, updated)
	if err != nil {
		return nil, err
	}
	for _, monitor := range monitors {
		if monitor.ID == id {
			return monitor, nil
		}
	}
	return nil, missingAPI("监控 " + id + " 不存在")
}

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
		items = slices.DeleteFunc(items, func(target config.Target) bool { return gone(target.ID) })
		// Other services may say they use the removed ones; those lines go with them.
		for i := range items {
			items[i].Depends = slices.DeleteFunc(items[i].Depends, func(item config.Dependency) bool { return gone(item.Target) })
		}
		return items
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
	for _, target := range cfg.Targets {
		if gone(target.ID) && cfg.Docs[target.ID] != "" {
			if _, err := b.options.Editor.SaveDoc(ctx, target.ID, ""); err != nil {
				b.options.Logger.Warn("Removed service's document was not deleted", "target", target.ID, "error", err)
			}
		}
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
		items, err := b.monitors(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if restricted, _, allowedMonitors := guestWebScope(ctx, cfg); restricted {
			filtered := make([]api.Monitor, 0, len(items))
			for _, item := range items {
				if allowedMonitors[item.ID] {
					filtered = append(filtered, item)
				}
			}
			return filtered, nil
		}
		return items, nil
	case api.AddMonitor:
		return b.addMonitor(ctx, cfg, r)
	case api.EditMonitor:
		return b.editMonitor(ctx, cfg, r)
	case api.RemoveMonitor:
		return b.removeMonitor(ctx, cfg, r.Params["id"], r.Query.Get("children") == "true")
	case api.ListBoards, api.GetBoard, api.CreateBoard, api.UpdateBoard, api.DeleteBoard:
		return b.boardOperation(ctx, cfg, op, r)
	case api.GetMonitor:
		if restricted, _, allowedMonitors := guestWebScope(ctx, cfg); restricted && !allowedMonitors[r.Params["id"]] {
			return nil, &api.Fault{Status: 403, Code: "forbidden_monitor", Message: "此服务未对内网访客开放"}
		}
		return b.monitorDetail(ctx, cfg, r.Params["id"])
	case api.GetMonitorDoc:
		if restricted, _, allowedMonitors := guestWebScope(ctx, cfg); restricted && !allowedMonitors[r.Params["id"]] {
			return nil, &api.Fault{Status: 403, Code: "forbidden_monitor", Message: "此服务未对内网访客开放"}
		}
		return b.monitorDoc(cfg, r.Params["id"])
	case api.PutMonitorDoc:
		return b.putMonitorDoc(ctx, cfg, r)
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
	case api.GetKnowledge:
		return b.knowledgeState(ctx, cfg), nil
	case api.PutKnowledge:
		var input api.AssistantKnowledgeInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		for id := range input.Docs {
			if _, ok := cfg.Docs[id]; !ok {
				return nil, invalidAPI("服务 " + id + " 没有文档")
			}
		}
		if err := b.setDocumentChoices(input.Docs, input.All); err != nil {
			return nil, err
		}
		b.refreshKnowledge()
		return b.knowledgeState(ctx, b.options.Config.Snapshot()), nil
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
