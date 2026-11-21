// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package winsvc

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type setupFixture struct {
	installer                           *Setup
	request                             SetupRequest
	program                             string
	definition                          Definition
	installed                           bool
	state                               State
	phase                               string
	changedBinding                      bool
	owned                               bool
	checkedUpgrade                      []bool
	effects                             []string
	receipt                             SetupReceipt
	initialized, joined, saved, removed int
	mutex                               sync.Mutex
}

func newSetupFixture(t *testing.T) *setupFixture {
	t.Helper()
	f := &setupFixture{program: filepath.Join(t.TempDir(), "program", "lgmon.exe"), request: SetupRequest{Home: filepath.Join(t.TempDir(), "home"), Role: "both", Listen: "127.0.0.1:7443", Confirmed: true}}
	var err error
	f.installer, err = NewSetup(SetupOptions{Service: f, Files: setupFilesFixture{f}, Pairer: setupPairFixture{f}, Binding: f.binding, Now: func() time.Time { return time.UnixMilli(2000000000000) }, Source: filepath.Join(t.TempDir(), "source.exe"), Program: f.program, Version: "fake-version", Lock: func(context.Context) (func(), error) {
		if !f.mutex.TryLock() {
			return nil, errors.New("fake installer is busy")
		}
		return f.mutex.Unlock, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *setupFixture) binding(context.Context) (Definition, bool, error) {
	value := f.definition
	if f.changedBinding && f.installed {
		value.Home = filepath.Join(f.request.Home, "different-home")
	}
	return value, f.installed, nil
}
func (f *setupFixture) Install(_ context.Context, d Definition) error {
	f.effects = append(f.effects, "register")
	if f.phase == "register" {
		return errors.New("fake register failure")
	}
	f.installed, f.definition = true, d
	f.definition.Executable = f.program
	return nil
}
func (f *setupFixture) Start(context.Context) error {
	f.effects = append(f.effects, "start")
	if f.phase == "start" || f.changedBinding {
		return errors.New("fake startup failure")
	}
	f.state = Running
	if f.phase == "not-running" {
		f.state = Stopped
	}
	return nil
}
func (f *setupFixture) Uninstall(context.Context) error {
	f.removed++
	if f.phase == "remove" {
		return errors.New("fake remove failure")
	}
	f.installed, f.state = false, Stopped
	return nil
}
func (f *setupFixture) Status(context.Context) (Status, error) {
	return Status{Installed: f.installed, State: f.state}, nil
}

type setupFilesFixture struct{ f *setupFixture }

func (f setupFilesFixture) Check(_ context.Context, _, _ string, upgrade bool) (bool, error) {
	f.f.checkedUpgrade = append(f.f.checkedUpgrade, upgrade)
	return true, nil
}
func (f setupFilesFixture) Owned(context.Context, string, string) (bool, error) {
	return f.f.owned, nil
}
func (f setupFilesFixture) Initialize(context.Context, string) error {
	f.f.effects = append(f.f.effects, "initialize")
	f.f.initialized++
	if f.f.phase == "initialize" {
		return errors.New("fake configuration failure")
	}
	return nil
}
func (f setupFilesFixture) SaveReceipt(_ context.Context, _ string, receipt SetupReceipt) error {
	f.f.effects = append(f.f.effects, "receipt")
	f.f.saved++
	if f.f.phase == "receipt" {
		return errors.New("fake receipt failure")
	}
	f.f.receipt = receipt
	return nil
}

type setupPairFixture struct{ f *setupFixture }

func (f setupPairFixture) Paired(context.Context, string, string) (bool, error) {
	return f.f.phase == "already-paired", nil
}
func (f setupPairFixture) Join(context.Context, string, string, string, string) error {
	f.f.effects = append(f.f.effects, "pair")
	f.f.joined++
	if f.f.phase == "pair" {
		return errors.New("fake pairing failure")
	}
	return nil
}

func TestSetupRolesPairBeforeRegistrationAndOnlyConfirmObservedRunning(t *testing.T) {
	for _, role := range []string{"collector", "monitor", "both"} {
		t.Run(role, func(t *testing.T) {
			f := newSetupFixture(t)
			f.request.Role = role
			if role == "collector" {
				f.request.Listen, f.request.Monitor = "", "fake-monitor:7443"
				f.request.Code, f.request.Fingerprint = "FAKE-ONCE", strings.Repeat("a", 16)
			}
			plan, err := f.installer.Check(context.Background(), f.request)
			if err != nil || !plan.ExistingConfig || len(f.effects) != 0 {
				t.Fatal("check produced an effect", plan, err, f.effects)
			}
			result, err := f.installer.Install(context.Background(), f.request)
			if err != nil || result.Status != "ok" || result.Service.State != Running || f.receipt.Role != role || f.receipt.InstalledAt != 2000000000000 {
				t.Fatal(result, err, f.receipt)
			}
			want := "initialize,register,start,receipt"
			if role == "collector" {
				want = "initialize,pair,register,start,receipt"
			}
			if strings.Join(f.effects, ",") != want {
				t.Fatal("incorrect effect ordering", f.effects)
			}
			data, _ := json.Marshal(f.receipt)
			output, _ := json.Marshal(result)
			if strings.Contains(string(append(data, output...)), "FAKE-ONCE") || strings.Contains(string(data), f.request.Fingerprint) && role == "collector" {
				t.Fatal("pairing secret escaped into installation receipt")
			}
		})
	}
}

func TestSetupPreconditionsRejectWithoutEffects(t *testing.T) {
	for _, kind := range []string{"confirmation", "existing-service", "bad-role", "root", "overlap", "bad-address", "missing-fingerprint", "changed-content", "busy"} {
		t.Run(kind, func(t *testing.T) {
			f := newSetupFixture(t)
			switch kind {
			case "confirmation":
				f.request.Confirmed = false
			case "existing-service":
				f.installed = true
			case "bad-role":
				f.request.Role = "shell"
			case "root":
				f.request.Home = filepath.VolumeName(f.request.Home) + string(filepath.Separator)
			case "overlap":
				f.request.Home = filepath.Join(filepath.Dir(f.program), "data")
			case "bad-address":
				f.request.Listen = "https://fake-monitor:7443"
			case "missing-fingerprint":
				f.request.Role, f.request.Listen, f.request.Monitor, f.request.Code = "collector", "", "fake-monitor:7443", "FAKE-ONCE"
			case "changed-content":
				f.request.Role, f.request.Listen, f.request.Monitor = "collector", "", "fake-monitor:7443"
				f.request.Code, f.request.Fingerprint, f.phase = "FAKE-ONCE", strings.Repeat("a", 16), "already-paired"
			case "busy":
				f.mutex.Lock()
				defer f.mutex.Unlock()
			}
			if result, err := f.installer.Install(context.Background(), f.request); err == nil || result.Status == "ok" || len(f.effects) != 0 {
				t.Fatal("invalid request produced an effect", result, err, f.effects)
			}
		})
	}
}

func TestSetupFailuresRollbackOnlyTheServiceRegisteredByThisAttempt(t *testing.T) {
	for _, phase := range []string{"initialize", "pair", "register", "start", "not-running", "receipt", "changed-binding"} {
		t.Run(phase, func(t *testing.T) {
			f := newSetupFixture(t)
			f.phase = phase
			if phase == "pair" {
				f.request.Role, f.request.Listen, f.request.Monitor = "collector", "", "fake-monitor:7443"
				f.request.Code, f.request.Fingerprint = "FAKE-ONCE", strings.Repeat("a", 16)
			}
			f.changedBinding = phase == "changed-binding"
			result, err := f.installer.Install(context.Background(), f.request)
			if err == nil || result.Status == "ok" {
				t.Fatal("failure reported installed", result, err)
			}
			wantRemoved := 0
			if phase == "start" || phase == "not-running" || phase == "receipt" {
				wantRemoved = 1
			}
			if f.removed != wantRemoved || (wantRemoved == 1 && (f.installed || result.Service.Installed)) {
				t.Fatal("wrong rollback boundary", f.effects, f.removed, err)
			}
			if phase == "changed-binding" && !f.installed {
				t.Fatal("changed binding was removed")
			}
		})
	}
}

func TestSetupCheckLetsAnUpgradePassOnlyForItsOwnService(t *testing.T) {
	f := newSetupFixture(t)
	if _, err := f.installer.Install(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	if _, err := f.installer.Check(context.Background(), f.request); err == nil {
		t.Fatal("a plain install passed over an existing service")
	}
	upgrade := f.request
	upgrade.Upgrade = true
	if _, err := f.installer.Check(context.Background(), upgrade); err != nil {
		t.Fatal("an upgrade of this installation was refused:", err)
	}
	f.changedBinding = true
	if _, err := f.installer.Check(context.Background(), upgrade); err == nil || !strings.Contains(err.Error(), "原位更新") {
		t.Fatal("an upgrade passed over another home's service:", err)
	}
	if f.removed != 0 || !f.installed {
		t.Fatal("checking changed the service")
	}
}

func TestSetupEndsSelfRunCopiesBeforeRegisteringTheService(t *testing.T) {
	f := newSetupFixture(t)
	f.installer.options.StopLocal = func(_ context.Context, home string) (int, error) {
		if home != f.request.Home {
			t.Errorf("stopped copies for %q", home)
		}
		f.effects = append(f.effects, "stop-local")
		return 1, nil
	}
	result, err := f.installer.Install(context.Background(), f.request)
	if err != nil || strings.Join(f.effects, ",") != "initialize,stop-local,register,start,receipt" || !strings.Contains(result.Message, "已结束 1 个") {
		t.Fatalf("effects=%v message=%q err=%v", f.effects, result.Message, err)
	}
	// A copy that cannot be ended would keep the ports, so nothing is registered.
	f = newSetupFixture(t)
	f.installer.options.StopLocal = func(context.Context, string) (int, error) { return 0, errors.New("fake access denied") }
	if _, err := f.installer.Install(context.Background(), f.request); err == nil || slices.Contains(f.effects, "register") {
		t.Fatalf("effects=%v err=%v", f.effects, err)
	}
}

func TestSetupCheckLetsAnUpdateResumeOnlyWhenTheLeftProgramIsThisInstallations(t *testing.T) {
	f := newSetupFixture(t)
	upgrade := f.request
	upgrade.Upgrade = true
	// The old service is already gone: an earlier attempt removed it and then failed, or it was removed by hand.
	for _, c := range []struct {
		name          string
		owned, update bool
		want          bool
	}{
		{"plain install", true, false, false},
		{"update of a program another home left", false, true, false},
		{"update of this installation's program", true, true, true},
	} {
		f.owned, f.checkedUpgrade = c.owned, nil
		request := f.request
		if c.update {
			request = upgrade
		}
		if _, err := f.installer.Check(context.Background(), request); err != nil {
			t.Fatal(c.name, err)
		}
		if len(f.checkedUpgrade) != 1 || f.checkedUpgrade[0] != c.want {
			t.Fatalf("%s: files checked with upgrade=%v, want %v", c.name, f.checkedUpgrade, c.want)
		}
	}
	if len(f.effects) != 0 {
		t.Fatal("checking had effects", f.effects)
	}
}

func TestSetupRemoveChecksBindingAndDoesNotChangeHome(t *testing.T) {
	f := newSetupFixture(t)
	if _, err := f.installer.Install(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	f.changedBinding = true
	if _, err := f.installer.Remove(context.Background(), f.request.Home, true); err == nil || f.removed != 0 {
		t.Fatal("mismatched service was touched", err)
	}
	f.changedBinding = false
	result, err := f.installer.Remove(context.Background(), f.request.Home, true)
	if err != nil || result.Status != "ok" || result.Service.Installed || f.initialized != 1 || f.saved != 1 || f.removed != 1 {
		t.Fatal("uninstall changed data or skipped service verification", result, err)
	}
	if _, err := f.installer.Remove(context.Background(), f.request.Home, true); err != nil || f.removed != 1 {
		t.Fatal("repeated uninstall repeated an effect", err)
	}
}

func TestSetupBindingRejectsUnrecognizedOrAmbiguousCommandLines(t *testing.T) {
	for _, args := range [][]string{{"fake.exe", "shell"}, {"fake.exe", "run", "--home", "one", "--home", "two"}, {"fake.exe", "run", "--role", "both", "--exec", "other"}, {"fake.exe", "run", "--home", "one"}} {
		if _, err := setupDefinition(args); err == nil {
			t.Fatal("accepted ambiguous binding", args)
		}
	}
	value, err := setupDefinition([]string{"fake.exe", "run", "--home", "fake-home", "--role", "both", "--listen", "127.0.0.1:7443", "--log-level", "info"})
	if err != nil || value.Home != "fake-home" || value.Role != "both" {
		t.Fatal(value, err)
	}
}
