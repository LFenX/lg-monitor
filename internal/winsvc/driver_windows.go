//go:build windows

package winsvc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/LFenX/lg-monitor/internal/secrets"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

type nativeDriver struct{}
type nativeManager struct {
	manager *mgr.Mgr
	write   bool
}
type nativeService struct{ service *mgr.Service }
type nativeEvents struct{}

func NativeController() (*Controller, error) {
	return NewController(Options{Driver: nativeDriver{}, Events: nativeEvents{}, Files: nativeFiles{policy: secrets.NativePathPolicy()}})
}

func (nativeDriver) Administrator() (bool, error) {
	return windows.GetCurrentProcessToken().IsElevated(), nil
}

func (nativeDriver) Connect(ctx context.Context, write bool) (Manager, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	access := uint32(windows.SC_MANAGER_CONNECT)
	if write {
		access |= windows.SC_MANAGER_CREATE_SERVICE
	}
	handle, err := windows.OpenSCManager(nil, nil, access)
	if err != nil {
		return nil, err
	}
	return &nativeManager{manager: &mgr.Mgr{Handle: handle}, write: write}, nil
}

func (m *nativeManager) Open() (Service, error) {
	name, err := windows.UTF16PtrFromString(Name)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.SERVICE_QUERY_STATUS)
	if m.write {
		access |= windows.SERVICE_START | windows.SERVICE_STOP | windows.DELETE
	}
	handle, err := windows.OpenService(m.manager.Handle, name, access)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil, ErrNotInstalled
	}
	if err != nil {
		return nil, err
	}
	return nativeService{&mgr.Service{Name: Name, Handle: handle}}, nil
}

func (m *nativeManager) Create(definition Definition) (Service, error) {
	service, err := m.manager.CreateService(Name, definition.Executable, mgr.Config{DisplayName: "lg-monitor", Description: "lg-monitor collection and monitoring", ServiceStartName: "LocalSystem", StartType: mgr.StartAutomatic, ErrorControl: mgr.ErrorNormal}, definition.Arguments()...)
	if err != nil {
		return nil, err
	}
	return nativeService{service}, nil
}

func (m *nativeManager) Close() error { return m.manager.Disconnect() }
func (s nativeService) Close() error  { return s.service.Close() }
func (s nativeService) Delete() error { return s.service.Delete() }
func (s nativeService) Start() error  { return s.service.Start() }
func (s nativeService) Stop() error   { _, err := s.service.Control(svc.Stop); return err }
func (s nativeService) Recovery(delay time.Duration) error {
	if err := s.service.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: delay}}, 86400); err != nil {
		return err
	}
	return s.service.SetRecoveryActionsOnNonCrashFailures(true)
}

func (s nativeService) Status() (Status, error) {
	status, err := s.service.Query()
	if err != nil {
		return Status{}, err
	}
	state := State("unknown")
	switch status.State {
	case svc.Stopped:
		state = Stopped
	case svc.StartPending:
		state = StartPending
	case svc.Running:
		state = Running
	case svc.StopPending:
		state = StopPending
	case svc.Paused:
		state = "paused"
	case svc.PausePending:
		state = "pause_pending"
	case svc.ContinuePending:
		state = "continue_pending"
	}
	return Status{Installed: true, State: state, PID: status.ProcessId}, nil
}

func (nativeEvents) Install() error {
	root, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\EventLog\Application`, registry.CREATE_SUB_KEY|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer root.Close()
	key, existed, err := registry.CreateKey(root, Name, registry.SET_VALUE)
	if err != nil {
		return err
	}
	if existed {
		key.Close()
		return fmt.Errorf("事件来源 lgmon 已经存在，不会覆盖，请检查原安装")
	}
	err = key.SetDWordValue("CustomSource", 1)
	if err == nil {
		err = key.SetExpandStringValue("EventMessageFile", `%SystemRoot%\System32\EventCreate.exe`)
	}
	if err == nil {
		err = key.SetDWordValue("TypesSupported", eventlog.Info|eventlog.Warning|eventlog.Error)
	}
	key.Close()
	if err != nil {
		return errors.Join(err, registry.DeleteKey(root, Name))
	}
	return nil
}
func (nativeEvents) Remove() error {
	err := eventlog.Remove(Name)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	return err
}
