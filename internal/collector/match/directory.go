package match

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
)

func (rule condition) hasProcess() bool {
	return rule.process != "" || rule.cmdline != nil || rule.exeDir != "" || rule.workDir != ""
}

func matchingDirectories(rule condition, process collector.Process) (bool, string) {
	unknown := ""
	if rule.exeDir != "" {
		directory, err := config.ExecutableDirectory(process.Exe)
		if err != nil {
			unknown = "exeDir（程序文件目录）"
		} else if !config.WindowsDirectoryContains(rule.exeDir, directory) {
			return false, ""
		}
	}
	if rule.workDir != "" {
		directory, err := config.ObservedWindowsDirectory(process.WorkDir)
		if err != nil {
			if unknown == "" {
				unknown = "workDir（工作目录）"
			}
		} else if !config.WindowsDirectoryContains(rule.workDir, directory) {
			return false, ""
		}
	}
	return true, unknown
}

func (m *Matcher) ResolveDirectories(ctx context.Context, snapshot collector.ProcessSnapshot, source collector.WorkingDirectorySource) ([]collector.Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("目录匹配已取消：%w", err)
	}
	processes := snapshot.Processes
	if source == nil || !slices.ContainsFunc(m.conditions, func(rule condition) bool { return rule.workDir != "" }) {
		return processes, nil
	}
	owned := map[int32][]collector.Port{}
	for _, port := range snapshot.Ports {
		owned[port.PID] = append(owned[port.PID], port)
	}
	services := map[string]collector.Service{}
	for _, service := range snapshot.Services {
		services[strings.ToLower(service.Name)] = service
	}
	resolved := map[collector.ProcessIdentity]string{}
	copied := false
	for _, rule := range m.conditions {
		if rule.workDir == "" || source == nil {
			continue
		}
		for i, process := range processes {
			if rule.process != "" && !matchesProcessName(rule.process, process.Name) {
				continue
			}
			if rule.cmdline != nil && (process.CmdlineUnavailable || !rule.cmdline.MatchString(process.Cmdline)) {
				continue
			}
			if rule.service != "" {
				service, known := services[strings.ToLower(rule.service)]
				if !snapshot.ServicesKnown || !known || service.State != collector.ServiceRunning || service.PID != process.PID {
					continue
				}
			}
			withoutWorkDir := rule
			withoutWorkDir.workDir = ""
			if matches, unknown := matchingDirectories(withoutWorkDir, process); !matches || unknown != "" {
				continue
			}
			if rule.port != 0 && !slices.ContainsFunc(owned[process.PID], func(port collector.Port) bool { return port.Port == uint32(rule.port) }) {
				continue
			}
			if !copied {
				// Shared snapshots retain only the observations collected by their owner.
				processes, copied = slices.Clone(processes), true
			}
			key := process.Identity()
			directory, exists := resolved[key]
			if !exists {
				var err error
				directory, err = source.WorkingDirectory(ctx, key)
				if failure := ctx.Err(); failure != nil {
					return nil, fmt.Errorf("目录匹配已取消：%w", failure)
				}
				if err != nil {
					directory = ""
				} else {
					directory, _ = config.ObservedWindowsDirectory(directory)
				}
				resolved[key] = directory
			}
			processes[i].WorkDir = directory
		}
	}
	return processes, nil
}
