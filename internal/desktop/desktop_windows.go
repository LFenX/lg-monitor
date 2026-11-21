//go:build windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

// Package desktop opens lgmon's pages on this person's desktop: in lgmon's own window when it is installed,
// otherwise in an Edge app window, otherwise in the default browser.
package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// WindowProgram is lgmon's launcher, built next to lgmon.exe. It is a separate program because the WebView2
// library initialises COM and pins the main thread for every process that links it, which a monitor must not.
const WindowProgram = "lgmon-window.exe"

// createNoWindow keeps a console-less child from flashing a console of its own.
const createNoWindow = 0x08000000

// OpenWindow shows target in lgmon's own window when lgmon-window.exe sits beside this program. dataDir, when set,
// holds the window's browser profile instead of the person's local application data.
func OpenWindow(target, dataDir string) error {
	if NoDesktop() {
		return fmt.Errorf("服务进程没有桌面，不打开 lgmon 窗口")
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("找不到 lgmon 程序位置：%w", err)
	}
	program := filepath.Join(filepath.Dir(self), WindowProgram)
	if !isFile(program) {
		return fmt.Errorf("没有找到 %s", WindowProgram)
	}
	args := []string{"--url", target}
	if dataDir != "" {
		args = append(args, "--data", dataDir)
	}
	command := exec.Command(program, args...)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	if err := command.Start(); err != nil {
		return fmt.Errorf("启动 lgmon 窗口失败：%w", err)
	}
	return command.Process.Release()
}

// OpenEdgeApp shows target in an Edge app window (no address bar or tabs) of the given size in logical pixels.
func OpenEdgeApp(target string, width, height int) error {
	edge := EdgeExecutable()
	if edge == "" {
		return fmt.Errorf("没有找到 Microsoft Edge")
	}
	command := exec.Command(edge, "--app="+target, "--window-size="+strconv.Itoa(width)+","+strconv.Itoa(height))
	if err := command.Start(); err != nil {
		return fmt.Errorf("启动 Edge 失败：%w", err)
	}
	return command.Process.Release()
}

// EdgeExecutable finds msedge.exe the way Windows itself does, then in its usual folders.
func EdgeExecutable() string {
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		key, err := registry.OpenKey(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		path, _, err := key.GetStringValue("")
		key.Close()
		if err == nil && isFile(path) {
			return path
		}
	}
	for _, variable := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
		if root := os.Getenv(variable); root != "" {
			if path := filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"); isFile(path) {
				return path
			}
		}
	}
	return ""
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// ShellOpen opens target with whatever this person uses for web pages.
func ShellOpen(target string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return fmt.Errorf("转换浏览器操作失败：%w", err)
	}
	address, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return fmt.Errorf("转换 lgmon 地址失败：%w", err)
	}
	if err := windows.ShellExecute(0, verb, address, nil, nil, windows.SW_SHOWNORMAL); err != nil {
		return fmt.Errorf("打开系统浏览器失败：%w", err)
	}
	return nil
}

// NoDesktop reports whether this process has no person's desktop to show a window on: it runs in session 0 (a
// service) or as LocalSystem. WebView2 refuses to start there and puts up an error box about its data directory.
func NoDesktop() bool {
	var session uint32
	if windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session) == nil && session == 0 {
		return true
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	return err == nil && user.User.Sid.IsWellKnown(windows.WinLocalSystemSid)
}
