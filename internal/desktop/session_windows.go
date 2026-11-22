// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package desktop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ActiveSessions prefers the physical console, followed by active remote desktops.
func ActiveSessions() ([]uint32, error) {
	var rows *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &rows, &count); err != nil {
		return nil, fmt.Errorf("枚举 Windows 登录会话失败：%w", err)
	}
	if rows != nil {
		defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(rows)))
	}
	var result []uint32
	console := windows.WTSGetActiveConsoleSessionId()
	for _, row := range unsafe.Slice(rows, count) {
		if row.State == windows.WTSActive && row.SessionID != 0 {
			result = append(result, row.SessionID)
		}
	}
	slices.Sort(result)
	if index := slices.Index(result, console); index > 0 {
		result[0], result[index] = result[index], result[0]
	}
	return result, nil
}

// OpenUpdateWindow runs the installed sibling window on the interactive desktop, with that user's proxy and profile.
func OpenUpdateWindow(ctx context.Context, target string, download bool) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位 lgmon 程序失败：%w", err)
	}
	program := filepath.Join(filepath.Dir(self), WindowProgram)
	if !isFile(program) {
		return fmt.Errorf("缺少 lgmon-window.exe，请修复安装后重试")
	}
	action := "check"
	if download {
		action = "download"
	}
	args := []string{"--url", target, "--update-action", action}
	if !NoDesktop() {
		if profile := os.Getenv("LGMON_WINDOW_DATA"); profile != "" {
			args = append(args, "--data", profile)
		}
		cmd := exec.CommandContext(ctx, program, args...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("更新小窗未响应，请检查 WebView2 或从开始菜单打开 lgmon 后重试：%w", err)
		}
		return nil
	}
	sessions, err := ActiveSessions()
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		return fmt.Errorf("请先登录监控器所在电脑的 Windows 桌面，再打开更新小窗")
	}
	var token windows.Token
	if err := windows.WTSQueryUserToken(sessions[0], &token); err != nil {
		return fmt.Errorf("无法在已登录用户的桌面打开更新小窗：%w", err)
	}
	defer token.Close()
	var env *uint16
	if err := windows.CreateEnvironmentBlock(&env, token, false); err != nil {
		return fmt.Errorf("读取桌面用户环境失败：%w", err)
	}
	defer windows.DestroyEnvironmentBlock(env)
	desktop, _ := windows.UTF16PtrFromString(`winsta0\default`)
	path, _ := windows.UTF16PtrFromString(program)
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(program))
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{program}, args...)))
	if err != nil {
		return fmt.Errorf("构造更新小窗参数失败：%w", err)
	}
	start := windows.StartupInfo{Desktop: desktop}
	start.Cb = uint32(unsafe.Sizeof(start))
	var process windows.ProcessInformation
	if err := windows.CreateProcessAsUser(token, path, line, nil, nil, false, windows.CREATE_UNICODE_ENVIRONMENT, env, dir, &start, &process); err != nil {
		return fmt.Errorf("在桌面用户会话中打开更新小窗失败：%w", err)
	}
	windows.CloseHandle(process.Thread)
	defer windows.CloseHandle(process.Process)
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("等待桌面更新小窗超时或已取消：%w", err)
		}
		result, err := windows.WaitForSingleObject(process.Process, 200)
		if err != nil {
			return fmt.Errorf("等待桌面更新小窗失败：%w", err)
		}
		if result == windows.WAIT_OBJECT_0 {
			var code uint32
			if err := windows.GetExitCodeProcess(process.Process, &code); err != nil {
				return fmt.Errorf("读取更新小窗结果失败：%w", err)
			}
			if code != 0 {
				return fmt.Errorf("桌面更新小窗未响应，请检查 WebView2 或从开始菜单打开 lgmon 后重试")
			}
			return nil
		}
	}
}
