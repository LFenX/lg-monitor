//go:build windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/LFenX/lg-monitor/internal/desktop"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	winDetachedProcess      = 0x00000008
	winCreateNewProcessGrp  = 0x00000200
	winCreateNoWindow       = 0x08000000
	detachedHelperProcFlags = winDetachedProcess | winCreateNewProcessGrp | winCreateNoWindow
)

// systemProxyURL reads Windows' per-user proxy settings so updates work behind tools like Clash
// even when HTTPS_PROXY is not inherited by the process.
func systemProxyURL() *url.URL {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer key.Close()
	if enabled, _, err := key.GetIntegerValue("ProxyEnable"); err != nil || enabled == 0 {
		return nil
	}
	server, _, err := key.GetStringValue("ProxyServer")
	if err != nil || strings.TrimSpace(server) == "" {
		return nil
	}
	for _, part := range strings.Split(server, ";") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(part), "https="); ok {
			server = value
			break
		}
	}
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}
	address, err := url.Parse(server)
	if err != nil || address.Host == "" {
		return nil
	}
	return address
}

func psSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func launchRestartHelper(serviceIdentity string, onDesktopCancel func()) (string, error) {
	if serviceIdentity == "LocalSystem" {
		// In service mode lgmon runs as LocalSystem, so a detached cmd running `net stop lgmon && net start lgmon`
		// cleanly stops, waits for flush, and immediately restarts the Windows service without UAC prompts.
		cmd := exec.Command("cmd.exe", "/c", "timeout /t 1 /nobreak >nul & net stop lgmon >nul 2>&1 & net start lgmon >nul 2>&1")
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedHelperProcFlags}
		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("启动服务重启进程失败：%w", err)
		}
		_ = cmd.Process.Release()
		return "service", nil
	}

	exe, err := os.Executable()
	if err != nil || exe == "" {
		return "", fmt.Errorf("无法定位当前 lgmon 程序路径：%w", err)
	}
	pid := os.Getpid()
	quotedArgs := make([]string, 0, len(os.Args)-1)
	for _, arg := range os.Args[1:] {
		quotedArgs = append(quotedArgs, psSingleQuote(arg))
	}
	var startCmd string
	if len(quotedArgs) > 0 {
		startCmd = fmt.Sprintf("Start-Process -FilePath %s -ArgumentList @(%s) -WindowStyle Hidden", psSingleQuote(exe), strings.Join(quotedArgs, ", "))
	} else {
		startCmd = fmt.Sprintf("Start-Process -FilePath %s -WindowStyle Hidden", psSingleQuote(exe))
	}
	script := fmt.Sprintf("Wait-Process -Id %d -Timeout 20 -ErrorAction SilentlyContinue; Start-Sleep -Milliseconds 300; %s", pid, startCmd)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedHelperProcFlags}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动重启辅助进程失败：%w", err)
	}
	_ = cmd.Process.Release()

	// Delay shutdown slightly so the HTTP 200 response reaches the browser before exiting gracefully.
	go func() {
		time.Sleep(300 * time.Millisecond)
		if onDesktopCancel != nil {
			onDesktopCancel()
		}
	}()
	return "process", nil
}

func launchUpdateInstaller(installerPath, serviceIdentity string) error {
	if _, err := os.Stat(installerPath); err != nil {
		return fmt.Errorf("找不到已下载的安装程序：%w", err)
	}
	args := []string{"--update"}
	flags := uint32(winCreateNewProcessGrp)
	if serviceIdentity == "LocalSystem" || desktop.NoDesktop() {
		// In Windows Service (Session 0) mode, run silently as LocalSystem for a completely unattended upgrade.
		args = append(args, "--silent")
		flags = detachedHelperProcFlags
	}
	cmd := exec.Command(installerPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动更新安装程序失败：%w", err)
	}
	_ = cmd.Process.Release()
	return nil
}

// isWindowsLockViolation is kept available for future Windows-specific diagnostics.
var _ = windows.ERROR_LOCK_VIOLATION
