//go:build windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import (
	"context"
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

type windowsServices struct {
	manager      *mgr.Mgr
	configBuffer []byte
}

func (nativeServices) Open(ctx context.Context, enumerate bool) (serviceSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	access := uint32(windows.SC_MANAGER_CONNECT)
	if enumerate {
		access |= windows.SC_MANAGER_ENUMERATE_SERVICE
	}
	handle, err := windows.OpenSCManager(nil, nil, access)
	if err != nil {
		return nil, err
	}
	return &windowsServices{manager: &mgr.Mgr{Handle: handle}, configBuffer: make([]byte, 1024)}, nil
}

func (s *windowsServices) Close() error { return s.manager.Disconnect() }
func (s *windowsServices) Names(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.manager.ListServices()
}

func (s *windowsServices) Read(ctx context.Context, name string) (Service, error) {
	row := Service{Name: name, DisplayName: name, State: ServiceUnknown, StartType: "unknown"}
	if err := ctx.Err(); err != nil {
		return row, err
	}
	wide, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return row, err
	}
	handle, err := windows.OpenService(s.manager.Handle, wide, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	configurationReadable := err == nil
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		handle, err = windows.OpenService(s.manager.Handle, wide, windows.SERVICE_QUERY_STATUS)
	}
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return row, errServiceMissing
	}
	if err != nil {
		return row, err
	}
	service := &mgr.Service{Name: name, Handle: handle}
	defer service.Close()
	status, err := service.Query()
	if err != nil {
		return row, err
	}
	row.PID = int32(status.ProcessId)
	switch status.State {
	case svc.Running:
		row.State = ServiceRunning
	case svc.Stopped:
		row.State = ServiceStopped
	case svc.StartPending:
		row.State = "start_pending"
	case svc.StopPending:
		row.State = "stop_pending"
	case svc.Paused:
		row.State = "paused"
	case svc.PausePending:
		row.State = "pause_pending"
	case svc.ContinuePending:
		row.State = "continue_pending"
	}
	if configurationReadable {
		displayName, startType, err := s.serviceConfig(handle)
		if err == nil {
			row.DisplayName, row.StartType = displayName, startType
		} else {
			configurationReadable = false
		}
	}
	if !configurationReadable {
		row.Error = "服务启动配置读取失败，请检查访问权限"
	}
	return row, nil
}

// SCM reads are frequent; the collector needs neither the description nor the service SID.
func (s *windowsServices) serviceConfig(handle windows.Handle) (displayName, startType string, err error) {
	var configuration *windows.QUERY_SERVICE_CONFIG
	for {
		configuration = (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&s.configBuffer[0]))
		var needed uint32
		err := windows.QueryServiceConfig(handle, configuration, uint32(len(s.configBuffer)), &needed)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || needed <= uint32(len(s.configBuffer)) {
			return "", "", err
		}
		s.configBuffer = make([]byte, needed)
	}
	startType = "unknown"
	switch configuration.StartType {
	case windows.SERVICE_AUTO_START:
		startType = "automatic"
		var delayed windows.SERVICE_DELAYED_AUTO_START_INFO
		var needed uint32
		if err := windows.QueryServiceConfig2(handle, windows.SERVICE_CONFIG_DELAYED_AUTO_START_INFO, (*byte)(unsafe.Pointer(&delayed)), uint32(unsafe.Sizeof(delayed)), &needed); err != nil {
			return "", "", err
		}
		if delayed.IsDelayedAutoStartUp != 0 {
			startType = "automatic_delayed"
		}
	case windows.SERVICE_DEMAND_START:
		startType = "manual"
	case windows.SERVICE_DISABLED:
		startType = "disabled"
	case windows.SERVICE_BOOT_START:
		startType = "boot"
	case windows.SERVICE_SYSTEM_START:
		startType = "system"
	}
	return windows.UTF16PtrToString(configuration.DisplayName), startType, nil
}
