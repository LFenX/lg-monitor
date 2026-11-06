//go:build windows

package collector

import (
	"context"
	"errors"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

type windowsServices struct{ manager *mgr.Mgr }

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
	return windowsServices{&mgr.Mgr{Handle: handle}}, nil
}

func (s windowsServices) Close() error { return s.manager.Disconnect() }
func (s windowsServices) Names(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.manager.ListServices()
}

func (s windowsServices) Read(ctx context.Context, name string) (Service, error) {
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
		configuration, err := service.Config()
		if err == nil {
			row.DisplayName = configuration.DisplayName
			switch configuration.StartType {
			case mgr.StartAutomatic:
				row.StartType = "automatic"
				if configuration.DelayedAutoStart {
					row.StartType = "automatic_delayed"
				}
			case mgr.StartManual:
				row.StartType = "manual"
			case mgr.StartDisabled:
				row.StartType = "disabled"
			case windows.SERVICE_BOOT_START:
				row.StartType = "boot"
			case windows.SERVICE_SYSTEM_START:
				row.StartType = "system"
			}
		} else {
			configurationReadable = false
		}
	}
	if !configurationReadable {
		row.Error = "服务启动配置读取失败，请检查访问权限"
	}
	return row, nil
}
