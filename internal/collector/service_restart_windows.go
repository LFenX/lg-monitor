//go:build windows

package collector

import (
	"context"
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

type NativeRestartServices struct{}
type nativeRestartHandle struct{ manager, service windows.Handle }

func (NativeRestartServices) Open(ctx context.Context, name string) (RestartServiceHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, fmt.Errorf("连接本机 SCM 失败：%w", err)
	}
	wide, err := windows.UTF16PtrFromString(name)
	if err != nil {
		windows.CloseServiceHandle(manager)
		return nil, fmt.Errorf("服务名称无效：%w", err)
	}
	service, err := windows.OpenService(manager, wide, windows.SERVICE_QUERY_STATUS|windows.SERVICE_START|windows.SERVICE_STOP)
	if err != nil {
		windows.CloseServiceHandle(manager)
		return nil, fmt.Errorf("打开精确服务失败：%w", err)
	}
	return &nativeRestartHandle{manager: manager, service: service}, nil
}
func (h *nativeRestartHandle) State() (string, error) {
	var status windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(h.service, &status); err != nil {
		return "", err
	}
	switch svc.State(status.CurrentState) {
	case svc.Running:
		return "running", nil
	case svc.Stopped:
		return "stopped", nil
	default:
		return "pending", nil
	}
}
func (h *nativeRestartHandle) Stop() error {
	var status windows.SERVICE_STATUS
	return windows.ControlService(h.service, windows.SERVICE_CONTROL_STOP, &status)
}
func (h *nativeRestartHandle) Start() error { return windows.StartService(h.service, 0, nil) }
func (h *nativeRestartHandle) Close() error {
	serviceErr := windows.CloseServiceHandle(h.service)
	managerErr := windows.CloseServiceHandle(h.manager)
	if serviceErr != nil {
		return serviceErr
	}
	return managerErr
}
