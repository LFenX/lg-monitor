package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/blueprint"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/transport"
)

func (b *APIBackend) processSnapshot(ctx context.Context, id string) (collector.ProcessList, error) {
	if b.options.Processes == nil {
		return collector.ProcessList{}, &api.Fault{Status: 503, Code: "collector_offline", Message: transport.ErrCollectorOffline.Error()}
	}
	now := b.options.Now()
	b.agent.mu.Lock()
	cached, ok := b.agent.snapshots[id]
	b.agent.mu.Unlock()
	if ok && now.Sub(cached.at) < processCacheTTL {
		return cached.list, nil
	}
	list, err := b.options.Processes.QueryProcesses(ctx, id, nil)
	if err != nil {
		switch {
		case errors.Is(err, transport.ErrCollectorOffline):
			return list, &api.Fault{Status: 503, Code: "collector_offline", Message: "这台电脑的采集器当前离线，无法读取进程和端口；服务列表仍可查看"}
		case errors.Is(err, transport.ErrCollectorOutdated):
			return list, &api.Fault{Status: 409, Code: "collector_outdated", Message: err.Error()}
		case errors.Is(err, context.DeadlineExceeded):
			return list, &api.Fault{Status: 504, Code: "collector_timeout", Message: "采集器十秒内未回应，请稍后重试"}
		default:
			return list, &api.Fault{Status: 502, Code: "collector_query_failed", Message: transport.ErrProcessSnapshot.Error()}
		}
	}
	b.agent.mu.Lock()
	if b.agent.snapshots == nil {
		b.agent.snapshots = map[string]processSnapshot{}
	}
	b.agent.snapshots[id] = processSnapshot{at: now, list: list}
	b.agent.mu.Unlock()
	return list, nil
}

func baseName(process string) string {
	return strings.TrimSuffix(strings.ToLower(process), ".exe")
}

// watchers indexes what the current targets already cover, so results can say "already monitored".
type watchers struct {
	ports     map[int]string
	services  map[string]string
	processes map[string]string
}

func coverage(cfg *config.Config, host api.GuideCollector) watchers {
	result := watchers{ports: map[int]string{}, services: map[string]string{}, processes: map[string]string{}}
	for _, target := range cfg.Targets {
		if !target.AppliesTo(host.ID, host.Hostname) || blueprint.ProbeOnly(target) {
			continue
		}
		if target.Match.Port != nil {
			result.ports[*target.Match.Port] = target.ID
		}
		if target.Match.Service != "" {
			result.services[strings.ToLower(target.Match.Service)] = target.ID
		}
		if target.Match.Process != "" && target.Match.Port == nil && target.Match.Cmdline == "" && target.Match.WorkDir == "" {
			result.processes[baseName(target.Match.Process)] = target.ID
		}
	}
	return result
}

func exploreNumber(values map[string][]string, name string, fallback, low, high int) (int, error) {
	list := values[name]
	if len(list) == 0 || list[0] == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(list[0])
	if err != nil || value < low || value > high {
		return 0, invalidAPI(name + " 必须是 " + strconv.Itoa(low) + "～" + strconv.Itoa(high) + " 的整数")
	}
	return value, nil
}

func (b *APIBackend) explore(ctx context.Context, cfg *config.Config, r api.Request) (any, error) {
	for name, values := range r.Query {
		if !slices.Contains([]string{"collector", "kind", "q", "state", "limit", "offset"}, name) || len(values) != 1 {
			return nil, invalidAPI("explore 只接受 collector、kind、q、state、limit、offset，且每个只能出现一次")
		}
	}
	kind := r.Query.Get("kind")
	if kind == "" {
		kind = "ports"
	}
	if !slices.Contains([]string{"ports", "services", "processes"}, kind) {
		return nil, invalidAPI("kind 必须是 ports、services 或 processes")
	}
	state := r.Query.Get("state")
	if state != "" && (kind != "services" || !slices.Contains([]string{"running", "stopped", "attention"}, state)) {
		return nil, invalidAPI("state 只用于 kind=services，取值 running、stopped 或 attention")
	}
	limit, err := exploreNumber(r.Query, "limit", 50, 1, 200)
	if err != nil {
		return nil, err
	}
	offset, err := exploreNumber(r.Query, "offset", 0, 0, 1000000)
	if err != nil {
		return nil, err
	}
	host, err := b.pickCollector(ctx, r.Query.Get("collector"))
	if err != nil {
		return nil, err
	}
	keyword := strings.ToLower(strings.TrimSpace(r.Query.Get("q")))
	covered := coverage(cfg, host)
	result := api.ExploreResult{Collector: host, Kind: kind, Counts: map[string]int{}, Items: []api.ExploreItem{}, Offset: offset, Limit: limit}
	var items []api.ExploreItem
	contains := func(values ...string) bool {
		if keyword == "" {
			return true
		}
		for _, value := range values {
			if strings.Contains(strings.ToLower(value), keyword) {
				return true
			}
		}
		return false
	}
	if kind == "services" {
		collectorID := host.ID
		rows, err := b.options.SQL.QueryParams(ctx, `SELECT s.name, s.state, s.start_type, s.pid, s.detail, s.ts FROM service_status s WHERE s.collector_id = :collector AND NOT EXISTS (SELECT 1 FROM service_status n WHERE n.collector_id = s.collector_id AND n.name = s.name AND n.ts > s.ts) ORDER BY s.name`, map[string]any{"collector": collectorID})
		if err != nil {
			return nil, err
		}
		for _, row := range rows.Rows {
			name, _ := row[0].(string)
			item := api.ExploreItem{Kind: "service", Name: name, DisplayName: name}
			item.State, _ = row[1].(string)
			item.StartType, _ = row[2].(string)
			if pid, ok := row[3].(int64); ok {
				item.PID = int32(pid)
			}
			if detail, ok := row[4].(string); ok {
				var parsed struct {
					DisplayName string `json:"displayName"`
				}
				if json.Unmarshal([]byte(detail), &parsed) == nil && parsed.DisplayName != "" {
					item.DisplayName = parsed.DisplayName
				}
			}
			if ts, ok := row[5].(int64); ok && ts > result.CollectedAt {
				result.CollectedAt = ts
			}
			if item.State == "deleted" || item.State == "missing" {
				continue
			}
			attention := item.State == "stopped" && (item.StartType == "automatic" || item.StartType == "auto")
			result.Total++
			result.Counts[item.State]++
			if attention {
				result.Counts["attention"]++
			}
			item.MonitoredBy = covered.services[strings.ToLower(name)]
			if item.MonitoredBy != "" {
				result.Counts["monitored"]++
			} else {
				item.Suggest = &api.ExploreSuggestion{Name: item.DisplayName, Service: name}
			}
			if !contains(item.Name, item.DisplayName) || (state == "attention" && !attention) || (state != "" && state != "attention" && item.State != state) {
				continue
			}
			items = append(items, item)
		}
		if result.Total == 0 {
			result.Notes = append(result.Notes, "还没有收到这台电脑的 Windows 服务数据；采集器启动约十秒后才有第一份清单。")
		}
	} else {
		list, err := b.processSnapshot(ctx, host.ID)
		if err != nil {
			return nil, err
		}
		result.CollectedAt = list.CollectedAt
		if list.Truncated {
			result.Notes = append(result.Notes, "进程很多，采集器只返回了其中一部分。")
		}
		if kind == "ports" {
			seen := map[int]bool{}
			for _, process := range list.Processes {
				for _, port := range process.Ports {
					number := int(port.Port)
					if seen[number] {
						continue
					}
					seen[number] = true
					result.Total++
					item := api.ExploreItem{Kind: "port", Name: strconv.Itoa(number), Port: number, Address: port.Address, Process: process.Name, PID: process.PID, Executable: process.Exe, Services: process.Services, RSS: process.RSS, CPUPercent: process.CPUPercent}
					local := strings.HasPrefix(port.Address, "127.") || port.Address == "::1"
					if local {
						result.Counts["localOnly"]++
					} else {
						result.Counts["network"]++
					}
					item.MonitoredBy = covered.ports[number]
					if item.MonitoredBy != "" {
						result.Counts["monitored"]++
					} else {
						value := number
						item.Suggest = &api.ExploreSuggestion{Name: baseName(process.Name) + " :" + item.Name, Port: &value}
					}
					if contains(item.Name, process.Name, process.Exe, strings.Join(process.Services, " ")) {
						items = append(items, item)
					}
				}
			}
			slices.SortFunc(items, func(a, c api.ExploreItem) int { return cmp.Compare(a.Port, c.Port) })
		} else {
			for _, process := range list.Processes {
				result.Total++
				item := api.ExploreItem{Kind: "process", Name: process.Name, Process: process.Name, PID: process.PID, Executable: process.Exe, CPUPercent: process.CPUPercent, RSS: process.RSS, Services: process.Services}
				if len(process.Ports) > 0 {
					item.Port, item.Address = int(process.Ports[0].Port), process.Ports[0].Address
					result.Counts["listening"]++
				}
				item.MonitoredBy = covered.processes[baseName(process.Name)]
				if item.MonitoredBy != "" {
					result.Counts["monitored"]++
				} else {
					item.Suggest = &api.ExploreSuggestion{Name: baseName(process.Name), Process: process.Name}
				}
				if contains(process.Name, process.Exe, strings.Join(process.Services, " ")) {
					items = append(items, item)
				}
			}
			// The heaviest processes are the ones worth a look first.
			slices.SortFunc(items, func(a, c api.ExploreItem) int {
				if a.RSS != c.RSS {
					return cmp.Compare(c.RSS, a.RSS)
				}
				return cmp.Compare(a.PID, c.PID)
			})
		}
	}
	result.Matched = len(items)
	if offset < len(items) {
		result.Items = items[offset:min(len(items), offset+limit)]
	}
	if result.Matched > offset+len(result.Items) {
		result.Notes = append(result.Notes, "还有 "+strconv.Itoa(result.Matched-offset-len(result.Items))+" 条未显示；用 q 缩小范围，或把 offset 设为 "+strconv.Itoa(offset+limit)+" 继续。")
	}
	return result, nil
}
