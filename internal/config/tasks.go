// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package config

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// Task Scheduler results that mean the task is fine: success, ready, running, not yet run, queued.
var TaskResultsOK = []uint32{0, 0x41300, 0x41301, 0x41303, 0x41325}

// TaskID is the case-insensitive identity the collector uses for a scheduled task path.
func TaskID(path string) string { return strings.ToLower(strings.TrimSpace(path)) }

// CheckTaskPath accepts a full Task Scheduler path such as \Backup\Nightly.
func CheckTaskPath(path string) error {
	if !strings.HasPrefix(path, `\`) || len(path) < 2 || len(path) > 1024 || strings.ContainsFunc(path, unicode.IsControl) || strings.Contains(path, `\\`) {
		return fmt.Errorf("计划任务需要完整路径，以 \\ 开头，例如 \\Backup\\Nightly；根目录下的任务写成 \\任务名")
	}
	return nil
}

func TaskResultOK(code uint32) bool { return slices.Contains(TaskResultsOK, code) }

// TaskResultText says in plain words what the last run result means.
func TaskResultText(code uint32) string {
	switch code {
	case 0:
		return "上次运行成功"
	case 0x41300:
		return "已就绪，等待下次运行"
	case 0x41301:
		return "正在运行"
	case 0x41303:
		return "还没有运行过"
	case 0x41325:
		return "已排队等待运行"
	case 0x41306:
		return "上次运行被手动终止"
	case 0x8004131F:
		return "上一次还没结束，这次没有启动"
	}
	return fmt.Sprintf("上次运行失败，结果代码 0x%08X", code)
}
