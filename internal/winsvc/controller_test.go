package winsvc

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeService struct {
	state                        State
	start, stop, deleted, closed int
	recovery                     time.Duration
	failRecovery, failDelete     error
}

func (s *fakeService) Status() (Status, error)            { return Status{Installed: true, State: s.state}, nil }
func (s *fakeService) Start() error                       { s.start++; s.state = Running; return nil }
func (s *fakeService) Stop() error                        { s.stop++; s.state = Stopped; return nil }
func (s *fakeService) Recovery(delay time.Duration) error { s.recovery = delay; return s.failRecovery }
func (s *fakeService) Delete() error                      { s.deleted++; return s.failDelete }
func (s *fakeService) Close() error                       { s.closed++; return nil }

type fakeManager struct {
	service    *fakeService
	installed  bool
	created    int
	definition Definition
	openError  error
}

func (m *fakeManager) Open() (Service, error) {
	if m.openError != nil {
		return nil, m.openError
	}
	if !m.installed {
		return nil, ErrNotInstalled
	}
	return m.service, nil
}
func (m *fakeManager) Create(definition Definition) (Service, error) {
	m.created++
	m.definition = definition
	return m.service, nil
}
func (*fakeManager) Close() error { return nil }

type fakeDriver struct {
	manager   *fakeManager
	admin     bool
	connected int
	writes    []bool
}

func (d *fakeDriver) Administrator() (bool, error) { return d.admin, nil }
func (d *fakeDriver) Connect(_ context.Context, write bool) (Manager, error) {
	d.connected++
	d.writes = append(d.writes, write)
	return d.manager, nil
}

type fakeEventSource struct {
	installed, removed int
	err                error
}

type fakeFiles struct {
	calls int
	err   error
}

func (f *fakeFiles) Prepare(_ context.Context, definition Definition) (Definition, error) {
	f.calls++
	definition.Executable = filepath.Join(filepath.Dir(definition.Executable), "protected", "lgmon.exe")
	return definition, f.err
}

func (s *fakeEventSource) Install() error { s.installed++; return s.err }
func (s *fakeEventSource) Remove() error  { s.removed++; return nil }

func controllerFixture(t *testing.T) (*Controller, *fakeDriver, *fakeEventSource, Definition) {
	t.Helper()
	driver := &fakeDriver{admin: true, manager: &fakeManager{service: &fakeService{state: Stopped}}}
	events := &fakeEventSource{}
	controller, err := NewController(Options{Driver: driver, Events: events, Files: &fakeFiles{}})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	return controller, driver, events, Definition{Executable: filepath.Join(root, "Program Files", "lgmon.exe"), Home: filepath.Join(root, "data with spaces"), Role: "both", LogLevel: "info"}
}

func TestServiceInstallationUsesAbsoluteArgumentsAndRecoveryWithoutStarting(t *testing.T) {
	c, driver, events, definition := controllerFixture(t)
	if err := c.Install(context.Background(), definition); err != nil {
		t.Fatal(err)
	}
	service := driver.manager.service
	if driver.manager.definition.Executable == definition.Executable || c.options.Files.(*fakeFiles).calls != 1 {
		t.Fatal("service did not use the prepared program copy")
	}
	if service.recovery != 10*time.Second || service.start != 0 || service.deleted != 0 || events.installed != 1 {
		t.Fatalf("installation policy: %+v %+v", service, events)
	}
	if !reflect.DeepEqual(driver.manager.definition.Arguments(), []string{"run", "--home", definition.Home, "--role", "both", "--log-level", "info"}) {
		t.Fatal("service split or changed absolute home arguments")
	}
}

func TestInstallChecksExistingServiceBeforeFilesAndNeverCreatesAfterFileFailure(t *testing.T) {
	c, driver, events, definition := controllerFixture(t)
	files := c.options.Files.(*fakeFiles)
	driver.manager.installed = true
	if err := c.Install(context.Background(), definition); err == nil || files.calls != 0 {
		t.Fatalf("existing installation touched files: %v", err)
	}
	driver.manager.installed = false
	files.err = errors.New("controlled file preparation failure")
	if err := c.Install(context.Background(), definition); !errors.Is(err, files.err) {
		t.Fatalf("file preparation failure lost: %v", err)
	}
	if driver.manager.created != 0 || events.installed != 0 {
		t.Fatal("failed file preparation reached SCM mutation")
	}
}

func TestInstallationFailureOnlyRollsBackNewService(t *testing.T) {
	for _, step := range []string{"existing", "open denied", "recovery", "event source", "rollback failed"} {
		t.Run(step, func(t *testing.T) {
			c, driver, events, definition := controllerFixture(t)
			sentinel := errors.New("controlled failure")
			switch step {
			case "existing":
				driver.manager.installed = true
			case "open denied":
				driver.manager.openError = sentinel
			case "recovery":
				driver.manager.service.failRecovery = sentinel
			case "event source":
				events.err = sentinel
			case "rollback failed":
				events.err, driver.manager.service.failDelete = sentinel, errors.New("delete failed")
			}
			err := c.Install(context.Background(), definition)
			if err == nil {
				t.Fatal("installation failure was ignored")
			}
			if step != "existing" && !errors.Is(err, sentinel) {
				t.Fatalf("lost installation cause: %v", err)
			}
			if step == "existing" || step == "open denied" {
				if driver.manager.created != 0 || driver.manager.service.deleted != 0 || events.installed != 0 {
					t.Fatal("existing resources were modified")
				}
			} else if driver.manager.service.deleted != 1 {
				t.Fatal("new service was not rolled back")
			}
			if events.removed != 0 {
				t.Fatal("installation rollback removed an event source it did not create")
			}
		})
	}
}

func TestServiceMutationNeedsAdminWhileStatusUsesReadOnlyAccess(t *testing.T) {
	c, driver, _, definition := controllerFixture(t)
	driver.admin = false
	for _, action := range []func(context.Context) error{func(ctx context.Context) error { return c.Install(ctx, definition) }, c.Start, c.Stop, c.Uninstall} {
		if err := action(context.Background()); err == nil || !strings.Contains(err.Error(), "管理员") {
			t.Fatalf("no admin gate: %v", err)
		}
	}
	if driver.connected != 0 {
		t.Fatal("mutation connected to SCM before checking privileges")
	}
	status, err := c.Status(context.Background())
	if err != nil || status.Installed || status.State != "not_installed" || !reflect.DeepEqual(driver.writes, []bool{false}) {
		t.Fatalf("read-only status: %+v %v", status, err)
	}
}

func TestServiceStartStopAreIdempotentAndUninstallStopsBeforeDeletion(t *testing.T) {
	c, driver, events, _ := controllerFixture(t)
	driver.manager.installed = true
	for _, action := range []func(context.Context) error{c.Start, c.Start, c.Stop, c.Stop, c.Start, c.Uninstall} {
		if err := action(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	service := driver.manager.service
	if service.start != 2 || service.stop != 2 || service.deleted != 1 || service.state != Stopped || events.removed != 1 {
		t.Fatalf("service lifecycle: %+v %+v", service, events)
	}
}
