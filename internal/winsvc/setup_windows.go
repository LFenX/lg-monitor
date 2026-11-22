//go:build windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package winsvc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/mgr"
)

const SetupMutex = `Global\lg-monitor-setup`

func LockSetup(ctx context.Context) (func(), error) {
	return LockSetupWithName(ctx, SetupMutex)
}

func LockSetupWithName(ctx context.Context, mutexName string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("安装操作已取消：%w", err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;BA)")
	if err != nil {
		return nil, fmt.Errorf("创建安装互斥权限失败：%w", err)
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
	name, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return nil, fmt.Errorf("安装互斥名称无效：%w", err)
	}
	// A Windows mutex belongs to its acquiring thread, not to the Go goroutine.
	runtime.LockOSThread()
	handle, err := windows.CreateMutex(&attributes, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("打开安装互斥失败：%w", err)
	}
	state, err := windows.WaitForSingleObject(handle, 0)
	if err != nil || (state != windows.WAIT_OBJECT_0 && state != windows.WAIT_ABANDONED) {
		windows.CloseHandle(handle)
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("另一个 lgmon 安装或卸载正在进行，请等待它结束")
	}
	return func() { windows.ReleaseMutex(handle); windows.CloseHandle(handle); runtime.UnlockOSThread() }, nil
}

func SetupProgram() (string, error) {
	directory, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return "", fmt.Errorf("读取安装程序目录失败：%w", err)
	}
	return filepath.Join(directory, "lg-monitor", "lgmon.exe"), nil
}

func InstalledDefinition(ctx context.Context) (Definition, bool, error) {
	if err := ctx.Err(); err != nil {
		return Definition{}, false, err
	}
	handle, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return Definition{}, false, fmt.Errorf("查询安装服务管理器失败：%w", err)
	}
	defer windows.CloseServiceHandle(handle)
	name, _ := windows.UTF16PtrFromString(Name)
	service, err := windows.OpenService(handle, name, windows.SERVICE_QUERY_CONFIG)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return Definition{}, false, nil
	}
	if err != nil {
		return Definition{}, false, fmt.Errorf("打开安装服务信息失败：%w", err)
	}
	s := &mgr.Service{Name: Name, Handle: service}
	defer s.Close()
	configuration, err := s.Config()
	if err != nil {
		return Definition{}, true, fmt.Errorf("读取安装服务定义失败：%w", err)
	}
	args, err := windows.DecomposeCommandLine(configuration.BinaryPathName)
	if err != nil {
		return Definition{}, true, fmt.Errorf("解析安装服务参数失败：%w", err)
	}
	definition, err := setupDefinition(args)
	return definition, true, err
}

// RestoreUpgradeProxy reads only the administrator-owned installer checkpoint for this home.
// SCM reads Environment at process creation, so restore it before the new service's first start.
func RestoreUpgradeProxy(home string) error {
	checkpoint, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\lg-monitor-upgrade`, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取升级代理记录失败：%w", err)
	}
	defer checkpoint.Close()
	entries, _, err := checkpoint.GetStringsValue(home)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取原服务代理失败：%w", err)
	}
	for _, entry := range entries {
		name, _, ok := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "ALL_PROXY":
			if ok {
				continue
			}
		}
		return fmt.Errorf("升级记录中存在无效代理变量")
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\lgmon`, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开新服务环境失败：%w", err)
	}
	defer key.Close()
	if err := key.SetStringsValue("Environment", entries); err != nil {
		return fmt.Errorf("写入新服务代理失败：%w", err)
	}
	return nil
}
