//go:build windows

package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// OpenAppWindow shows lgmon's small start page in a window of its own. Edge's app mode has no address bar or
// tabs, so the page reads as part of lgmon rather than one more browser tab; it ships with Windows 10 and 11,
// so nothing is added to lgmon. Without Edge the default browser opens the same page.
func OpenAppWindow(ctx context.Context, address string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !trayPanelURL(address) {
		return fmt.Errorf("lgmon 地址必须为不含凭据、查询参数和片段的 HTTP/HTTPS 地址")
	}
	target := StartPageURL(address)
	if edge := edgeExecutable(); edge != "" {
		command := exec.Command(edge, "--app="+target, "--window-size=460,800")
		if err := command.Start(); err == nil {
			return command.Process.Release()
		}
	}
	return shellOpen(target)
}

// StartPageURL is the small page shown when lgmon is opened.
func StartPageURL(address string) string {
	return strings.TrimRight(address, "/") + "/#/start"
}

func edgeExecutable() string {
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

func shellOpen(target string) error {
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

func (NativeTrayOpener) Window(ctx context.Context, address string) error {
	return OpenAppWindow(ctx, address)
}
