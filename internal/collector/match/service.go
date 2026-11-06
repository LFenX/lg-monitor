package match

import (
	"fmt"
	"strings"

	"github.com/LFenX/lg-monitor/internal/collector"
)

func (m *Matcher) MatchWithServices(processes []collector.Process, ports []collector.Port, services []collector.Service) []Result {
	results := m.Match(processes, ports)
	known := make(map[string]collector.Service, len(services))
	for _, service := range services {
		known[strings.ToLower(service.Name)] = service
	}
	for i, rule := range m.conditions {
		if rule.service == "" {
			continue
		}
		service, exists := known[strings.ToLower(rule.service)]
		if !exists {
			service = collector.Service{Name: rule.service, State: collector.ServiceMissing}
		}
		results[i] = serviceResult(rule, service, processes, ports)
	}
	return results
}

func serviceResult(rule condition, service collector.Service, processes []collector.Process, ports []collector.Port) Result {
	result := Result{TargetID: rule.id, State: collector.StateUnknown, Service: service.State, ServiceName: service.Name, Process: collector.ProcessUnknown, Processes: []collector.Process{}, Ports: []collector.Port{}}
	if rule.port != 0 {
		result.Port = collector.PortUnknown
	}
	if service.State == collector.ServiceUnknown {
		result.Reason = "服务状态不可读取，请检查采集器的服务查询权限"
		return result
	}
	if service.State == collector.ServiceMissing || service.State == collector.ServiceDeleted || service.State == collector.ServiceStopped {
		result.State, result.Process = collector.StateDown, collector.ProcessMissing
		if rule.port != 0 {
			result.Port = collector.PortClosed
		}
		result.Reason = "服务 " + service.Name + " 已停止"
		if service.State != collector.ServiceStopped {
			result.Reason = "服务 " + service.Name + " 不存在"
		}
		return result
	}
	for _, process := range processes {
		if service.PID > 0 && process.PID == service.PID {
			result.Processes = append(result.Processes, process)
		}
	}
	for _, port := range ports {
		if service.PID > 0 && port.PID == service.PID {
			result.Ports = append(result.Ports, port)
		}
	}
	if len(result.Processes) > 0 {
		result.Process = collector.ProcessFound
	}
	if service.State != collector.ServiceRunning {
		result.Reason = fmt.Sprintf("服务 %s 的状态为 %s，请等待状态稳定", service.Name, service.State)
		if service.State == "paused" {
			result.State = collector.StateDown
		}
		return result
	}
	if rule.process == "" && rule.cmdline == nil && rule.port == 0 {
		result.State, result.Reason = collector.StateUp, "服务 "+service.Name+" 正在运行"
		if len(result.Processes) == 0 {
			result.Reason += "，进程信息不可用"
		}
		return result
	}
	if service.PID <= 0 || (len(result.Processes) == 0 && (rule.process != "" || rule.cmdline != nil)) {
		result.Reason = "服务正在运行，但服务进程信息尚不可用，请等待下次采集"
		return result
	}
	rule.service = ""
	subset := (&Matcher{conditions: []condition{rule}}).Match(result.Processes, result.Ports)[0]
	subset.Service, subset.ServiceName = service.State, service.Name
	if subset.Process == "" {
		subset.Process = result.Process
	}
	return subset
}
