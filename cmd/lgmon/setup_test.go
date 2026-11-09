//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/winsvc"
	"github.com/spf13/cobra"
)

type setupTestPaths struct{ fail string }

func (p setupTestPaths) Check(path string) error {
	if path == p.fail {
		return errors.New("fake ACL denied")
	}
	return nil
}
func (setupTestPaths) ProtectTree(path string, _ bool) error { return os.MkdirAll(path, 0o700) }

type setupTestService struct {
	definition winsvc.Definition
	program    string
	installed  bool
	calls      []string
	state      winsvc.State
	joined     int
	paired     bool
}

func (s *setupTestService) Install(_ context.Context, definition winsvc.Definition) error {
	s.calls = append(s.calls, "register")
	if s.installed {
		return errors.New("fake already installed")
	}
	if err := os.MkdirAll(filepath.Dir(s.program), 0o700); err != nil {
		return err
	}
	data, err := os.ReadFile(definition.Executable)
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.program, data, 0o600); err != nil {
		return err
	}
	s.definition, s.installed = definition, true
	s.definition.Executable = s.program
	return nil
}
func (s *setupTestService) Start(context.Context) error {
	s.calls = append(s.calls, "start")
	s.state = winsvc.Running
	return nil
}
func (s *setupTestService) Uninstall(context.Context) error {
	s.calls = append(s.calls, "remove")
	s.installed, s.state = false, winsvc.Stopped
	return nil
}
func (s *setupTestService) Status(context.Context) (winsvc.Status, error) {
	return winsvc.Status{Installed: s.installed, State: s.state}, nil
}
func (s *setupTestService) Paired(context.Context, string, string) (bool, error) {
	return s.paired, nil
}
func (s *setupTestService) Join(context.Context, string, string, string, string) error {
	s.joined++
	s.paired = true
	return nil
}

type setupCommandFixture struct {
	source, home string
	service      *setupTestService
	deps         setupDependencies
}

func newSetupCommandFixture(t *testing.T) *setupCommandFixture {
	t.Helper()
	root := t.TempDir()
	f := &setupCommandFixture{source: filepath.Join(root, "payload", "lgmon.exe"), home: filepath.Join(root, "home"), service: &setupTestService{program: filepath.Join(root, "program", "lgmon.exe")}}
	if err := os.MkdirAll(filepath.Dir(f.source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.source, []byte("fake executable bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := setupTestPaths{}
	f.deps = setupDependencies{Administrator: func() (bool, error) { return true, nil }, Paths: paths, Guard: setupGuardPath, Panel: func(string) (string, error) { return "http://127.0.0.1:7444", nil }, Factory: func(context.Context) (*winsvc.Setup, error) {
		return winsvc.NewSetup(winsvc.SetupOptions{Service: f.service, Files: setupFileStore{Source: f.source, Paths: paths, Guard: setupGuardPath}, Pairer: f.service, Binding: func(context.Context) (winsvc.Definition, bool, error) {
			return f.service.definition, f.service.installed, nil
		}, Lock: func(context.Context) (func(), error) { return func() {}, nil }, Now: func() time.Time { return time.UnixMilli(2000000000000) }, Source: f.source, Program: f.service.program, Version: "fake-version"})
	}}
	return f
}

func (f *setupCommandFixture) run(input string, args ...string) (string, error) {
	var output bytes.Buffer
	root := &cobra.Command{Use: "lgmon", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("home", f.home, "fake home")
	root.AddCommand(newSetupCommand(f.deps))
	root.SetArgs(append([]string{"setup"}, args...))
	root.SetIn(strings.NewReader(input))
	root.SetOut(&output)
	root.SetErr(&output)
	err := root.Execute()
	return output.String(), err
}

func TestSetupCLIConfirmationAdminAndReadonlyChecksHaveNoEffects(t *testing.T) {
	f := newSetupCommandFixture(t)
	for _, args := range [][]string{{"install", "--json"}, {"remove", "--json"}, {"install", "--code", "FAKE_SECRET", "--confirm"}} {
		if _, err := f.run("", args...); err == nil || len(f.service.calls) != 0 {
			t.Fatal("unconfirmed request touched service", args, err)
		}
	}
	if _, err := os.Stat(f.home); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unconfirmed request created home", err)
	}
	if result, err := f.run("", "check", "--json"); err != nil || !strings.Contains(result, `"role":"both"`) {
		t.Fatal(result, err)
	}
	if _, err := os.Stat(f.home); !errors.Is(err, os.ErrNotExist) || len(f.service.calls) != 0 {
		t.Fatal("read-only check created state", err)
	}
	f.deps.Administrator = func() (bool, error) { return false, nil }
	if _, err := f.run("", "install", "--confirm", "--json"); err == nil || len(f.service.calls) != 0 {
		t.Fatal("non-administrator reached setup", err)
	}
}

func TestSetupCLIFreshConfigValidAndUninstallPreservesExistingFiles(t *testing.T) {
	f := newSetupCommandFixture(t)
	result, err := f.run("", "install", "--confirm", "--json")
	if err != nil || !strings.Contains(result, `"state":"running"`) || strings.Join(f.service.calls, ",") != "register,start" {
		t.Fatal(result, err, f.service.calls)
	}
	cfg, err := config.Load(os.DirFS(filepath.Join(f.home, "config")))
	if err != nil || cfg.Settings.RemoteOperations.Enabled || len(cfg.Targets) != 0 || len(cfg.Notifiers) != 0 || len(cfg.Rules) != 0 {
		t.Fatal("invalid or unsafe bootstrap", err)
	}
	receiptPath := filepath.Join(f.home, "data", "setup", "installation.json")
	before, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(f.home, "config", "targets.yaml")
	const preserved = "# actual user comment\ntargets:\n  - id: fake-target\n    name: 虚构目标\n    match:\n      process: fake.exe\n"
	if err := os.WriteFile(custom, []byte(preserved), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = f.run("", "remove", "--confirm", "--json")
	if err != nil || f.service.installed {
		t.Fatal(result, err)
	}
	after, err := os.ReadFile(receiptPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("uninstall changed receipt", err)
	}
	if data, err := os.ReadFile(custom); err != nil || string(data) != preserved {
		t.Fatal("uninstall changed user config", err)
	}
	// Reinstallation is only permitted after the package's own program files have been removed.
	if _, err := f.run("", "install", "--confirm", "--json"); err == nil {
		t.Fatal("loose existing program was overwritten")
	}
	if err := os.Remove(f.service.program); err != nil {
		t.Fatal(err)
	}
	if result, err := f.run("", "install", "--confirm", "--json"); err != nil {
		t.Fatal(result, err)
	}
	if data, err := os.ReadFile(custom); err != nil || string(data) != preserved {
		t.Fatal("reinstallation overwrote user config", err)
	}
}

func TestSetupCLIPairInputOnlyPrivateFileOrStdinAndNeverLeaks(t *testing.T) {
	for _, fileInput := range []bool{false, true} {
		f := newSetupCommandFixture(t)
		input := `{"code":"FAKE_PAIRING_PRIVATE","fingerprint":"0123456789abcdef"}`
		args := []string{"install", "--role", "collector", "--monitor", "fake-monitor:7443", "--confirm", "--json"}
		path := filepath.Join(t.TempDir(), "pair.json")
		if fileInput {
			if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
				t.Fatal(err)
			}
			args = append(args, "--pair-input", path)
		} else {
			args = append(args, "--pair-stdin")
		}
		result, err := f.run(input, args...)
		if err != nil || strings.Contains(result, "FAKE_PAIRING_PRIVATE") || f.service.joined != 1 {
			t.Fatal(result, err, f.service.joined)
		}
		if fileInput {
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("consumed pairing input survived", err)
			}
		}
		receipt, err := os.ReadFile(filepath.Join(f.home, "data", "setup", "installation.json"))
		if err != nil || strings.Contains(string(receipt), "FAKE_PAIRING_PRIVATE") || strings.Contains(string(receipt), "0123456789abcdef") {
			t.Fatal("pairing input escaped into receipt", err)
		}
	}
	f := newSetupCommandFixture(t)
	private := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(private, []byte(`{"code":"FAKE_PRIVATE","fingerprint":"0123456789abcdef"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f.deps.Paths = setupTestPaths{fail: private}
	result, err := f.run("", "install", "--role", "collector", "--monitor", "fake-monitor:7443", "--confirm", "--pair-input", private, "--json")
	if err == nil || strings.Contains(result, "FAKE_PRIVATE") || len(f.service.calls) != 0 {
		t.Fatal("unsafe input reached service or leaked", result, err)
	}
	if _, err := os.Stat(private); err != nil {
		t.Fatal("rejected input was deleted", err)
	}
	for _, input := range []string{`null`, `{"code":"FAKE_PRIVATE","exec":"cmd"}`, `{"code":{},"fingerprint":"FAKE_PRIVATE"}`, `{}` + `{}`} {
		if _, err := readSetupPairInput(strings.NewReader(input)); err == nil || strings.Contains(err.Error(), "FAKE_PRIVATE") {
			t.Fatal("invalid input was accepted or echoed", err)
		}
	}
}

func TestSetupInboxRefusesExistingDirectoryAndDoesNotCreateHome(t *testing.T) {
	f := newSetupCommandFixture(t)
	directory := filepath.Join(t.TempDir(), "private-input")
	if output, err := f.run("", "inbox", "--directory", directory, "--json"); err != nil || !json.Valid([]byte(output)) {
		t.Fatal(output, err)
	}
	if _, err := f.run("", "inbox", "--directory", directory); err == nil {
		t.Fatal("inbox took over an existing directory")
	}
	if _, err := os.Stat(f.home); !errors.Is(err, os.ErrNotExist) || len(f.service.calls) != 0 {
		t.Fatal("inbox initialized application state", err)
	}
}
