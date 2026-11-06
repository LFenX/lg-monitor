package match

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
)

type Result struct {
	TargetID  string              `json:"targetId"`
	State     string              `json:"state"`
	Processes []collector.Process `json:"processes"`
	Ports     []collector.Port    `json:"ports"`
	Reason    string              `json:"reason"`
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
			compiled, err := regexp.Compile(target.Match.Cmdline)
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
	owned := make(map[int32][]collector.Port)
	for _, port := range ports {
		owned[port.PID] = append(owned[port.PID], port)
	}
	results := make([]Result, 0, len(m.conditions))
	for _, rule := range m.conditions {
		result := Result{TargetID: rule.id, State: "down", Processes: []collector.Process{}, Ports: []collector.Port{}, Reason: "没有找到匹配的进程"}
		if rule.service != "" {
			// M0 accepts service configuration but must not silently ignore its condition.
			result.Reason = "服务名匹配将在 M1-07 提供，当前未执行此目标的匹配"
			results = append(results, result)
			continue
		}
		best := -1
		for _, process := range processes {
			score := 0
			if rule.process != "" && !strings.EqualFold(rule.process, process.Name) {
				continue
			}
			if rule.process != "" {
				score++
			}
			if rule.cmdline != nil && !rule.cmdline.MatchString(process.Cmdline) {
				if score > best {
					result.Reason = "进程命令行不匹配"
					if score > 0 {
						result.Reason = "进程名匹配，但命令行不匹配"
					}
					best = score
				}
				continue
			}
			if rule.cmdline != nil {
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
			result.Processes = append(result.Processes, process)
			result.Ports = append(result.Ports, matchedPorts...)
		}
		if len(result.Processes) > 0 {
			result.State = "up"
			result.Reason = fmt.Sprintf("匹配到 %d 个进程", len(result.Processes))
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
