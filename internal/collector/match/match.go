package match

import (
	"cmp"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
)

type Result struct {
	TargetID    string              `json:"targetId"`
	State       string              `json:"state"`
	Process     string              `json:"process"`
	Port        string              `json:"port"`
	Processes   []collector.Process `json:"processes"`
	Ports       []collector.Port    `json:"ports"`
	Reason      string              `json:"reason"`
	Service     string              `json:"service,omitempty"`
	ServiceName string              `json:"serviceName,omitempty"`
}

type condition struct {
	id      string
	process string
	cmdline *regexp.Regexp
	port    int
	service string
}

type Matcher struct{ conditions []condition }

func New(targets []config.Target) (*Matcher, error) {
	matcher := &Matcher{}
	seen := make(map[string]bool, len(targets))
	for _, target := range targets {
		if target.ID == "" || seen[target.ID] {
			return nil, fmt.Errorf("监控目标 ID 不能为空或重复：%s", target.ID)
		}
		seen[target.ID] = true
		rule := condition{id: target.ID, process: target.Match.Process, service: target.Match.Service}
		if target.Match.Cmdline != "" {
			compiled, err := regexp.Compile("(?i)" + target.Match.Cmdline)
			if err != nil {
				return nil, fmt.Errorf("目标 %s 的命令行正则无效，请检查括号和转义：%w", target.ID, err)
			}
			rule.cmdline = compiled
		}
		if target.Match.Port != nil {
			rule.port = *target.Match.Port
			if rule.port < 1 || rule.port > 65535 {
				return nil, fmt.Errorf("目标 %s 的端口必须在 1 到 65535 之间", target.ID)
			}
		}
		if rule.process == "" && rule.cmdline == nil && rule.port == 0 && rule.service == "" {
			return nil, fmt.Errorf("目标 %s 至少需要一个匹配条件", target.ID)
		}
		matcher.conditions = append(matcher.conditions, rule)
	}
	return matcher, nil
}

func (m *Matcher) Match(processes []collector.Process, ports []collector.Port) []Result {
	knownPIDs := make(map[int32]bool, len(processes))
	for _, process := range processes {
		knownPIDs[process.PID] = true
	}
	owned := make(map[int32][]collector.Port)
	for _, port := range ports {
		owned[port.PID] = append(owned[port.PID], port)
	}
	results := make([]Result, 0, len(m.conditions))
	for _, rule := range m.conditions {
		result := Result{TargetID: rule.id, State: collector.StateDown, Processes: []collector.Process{}, Ports: []collector.Port{}, Reason: "没有找到匹配的进程"}
		result.Process, result.Port = componentFacts(rule, processes, ports, owned)
		if rule.service != "" {
			result.State = collector.StateUnknown
			result.Service, result.ServiceName = collector.ServiceUnknown, rule.service
			result.Reason = "服务状态不可读取，请检查采集器的服务查询权限"
			results = append(results, result)
			continue
		}
		best := -1
		unknownReason := ""
		for _, process := range processes {
			score := 0
			if rule.process != "" && !matchesProcessName(rule.process, process.Name) {
				continue
			}
			if rule.process != "" {
				score++
			}
			cmdlineUnknown := rule.cmdline != nil && process.CmdlineUnavailable
			if rule.cmdline != nil && !cmdlineUnknown && !rule.cmdline.MatchString(process.Cmdline) {
				if score > best {
					result.Reason = "进程命令行不匹配"
					if score > 0 {
						result.Reason = "进程名匹配，但命令行不匹配"
					}
					best = score
				}
				continue
			}
			if rule.cmdline != nil && !cmdlineUnknown {
				score++
			}
			matchedPorts := make([]collector.Port, 0)
			for _, port := range owned[process.PID] {
				if rule.port == 0 || port.Port == uint32(rule.port) {
					matchedPorts = append(matchedPorts, port)
				}
			}
			if rule.port != 0 && len(matchedPorts) == 0 {
				if score >= best {
					prefix := "匹配条件已满足，但该进程"
					if rule.process != "" && rule.cmdline == nil {
						prefix = "进程名匹配，但"
					}
					result.Reason = fmt.Sprintf("%s没有在监听 %d", prefix, rule.port)
					best = score
				}
				continue
			}
			if cmdlineUnknown {
				unknownReason = fmt.Sprintf("进程 %s（PID %d）的命令行读取不到（权限不足）：请以管理员身份运行，或者去掉 cmdline 条件", process.Name, process.PID)
				continue
			}
			result.Processes = append(result.Processes, process)
			result.Ports = append(result.Ports, matchedPorts...)
		}
		var unavailablePIDs []int32
		if rule.process == "" && rule.cmdline == nil && rule.port != 0 {
			seen := make(map[int32]bool)
			for _, listener := range ports {
				if listener.Port != uint32(rule.port) || knownPIDs[listener.PID] {
					continue
				}
				result.Ports = append(result.Ports, listener)
				if !seen[listener.PID] {
					unavailablePIDs = append(unavailablePIDs, listener.PID)
					seen[listener.PID] = true
				}
			}
			if len(result.Ports) == 0 {
				result.Reason = fmt.Sprintf("没有在监听 %d", rule.port)
			}
		}
		if len(result.Processes) > 0 || len(result.Ports) > 0 {
			result.State = collector.StateUp
			result.Reason = fmt.Sprintf("匹配到 %d 个进程", len(result.Processes))
			if len(unavailablePIDs) > 0 {
				slices.Sort(unavailablePIDs)
				pids := make([]string, len(unavailablePIDs))
				for i, pid := range unavailablePIDs {
					pids[i] = fmt.Sprint(pid)
				}
				result.Reason = fmt.Sprintf("端口 %d 由 PID %s 监听，进程信息不可用", rule.port, strings.Join(pids, "、"))
			}
		} else if unknownReason != "" {
			result.State, result.Reason = collector.StateUnknown, unknownReason
		}
		slices.SortFunc(result.Processes, func(a, b collector.Process) int {
			if order := cmp.Compare(a.PID, b.PID); order != 0 {
				return order
			}
			return cmp.Compare(a.CreatedAt, b.CreatedAt)
		})
		slices.SortFunc(result.Ports, func(a, b collector.Port) int {
			if order := cmp.Compare(a.PID, b.PID); order != 0 {
				return order
			}
			if order := cmp.Compare(a.Port, b.Port); order != 0 {
				return order
			}
			return cmp.Compare(a.LocalAddress, b.LocalAddress)
		})
		results = append(results, result)
	}
	return results
}

func matchesProcessName(pattern, name string) bool {
	return strings.EqualFold(pattern, name) || (path.Ext(pattern) == "" && strings.EqualFold(pattern+".exe", name))
}

func componentFacts(rule condition, processes []collector.Process, ports []collector.Port, owned map[int32][]collector.Port) (process, port string) {
	hasProcess := rule.process != "" || rule.cmdline != nil
	if hasProcess {
		process = collector.ProcessMissing
	}
	if rule.port != 0 {
		port = collector.PortClosed
	}
	if rule.service != "" {
		if hasProcess {
			process = collector.ProcessUnknown
		}
		if rule.port != 0 {
			port = collector.PortUnknown
		}
		return process, port
	}
	if !hasProcess {
		for _, listener := range ports {
			if rule.port != 0 && listener.Port == uint32(rule.port) {
				port = collector.PortListening
				break
			}
		}
		return process, port
	}
	for _, candidate := range processes {
		if rule.process != "" && !matchesProcessName(rule.process, candidate.Name) {
			continue
		}
		unknown := rule.cmdline != nil && candidate.CmdlineUnavailable
		if rule.cmdline != nil && !unknown && !rule.cmdline.MatchString(candidate.Cmdline) {
			continue
		}
		if unknown {
			if process != collector.ProcessFound {
				process = collector.ProcessUnknown
			}
		} else {
			process = collector.ProcessFound
		}
		for _, listener := range owned[candidate.PID] {
			if rule.port == 0 || listener.Port != uint32(rule.port) {
				continue
			}
			if !unknown {
				port = collector.PortListening
			} else if port != collector.PortListening {
				port = collector.PortUnknown
			}
		}
	}
	return process, port
}
