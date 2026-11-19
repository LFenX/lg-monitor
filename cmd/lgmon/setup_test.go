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
	"golang.org/x/sys/windows"
	"golang.org/x/text/encoding/unicode"
)

type setupTestPaths struct{ fail string }

type setupObservedPaths struct {
	setupTestPaths
	protected int
}

func (p *setupObservedPaths) ProtectTree(path string, readable bool) error {
	p.protected++
	return p.setupTestPaths.ProtectTree(path, readable)
}

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
	// An update may pass over this installation's own service and program; a plain install may not.
	if _, err := f.run("", "check", "--json"); err == nil {
		t.Fatal("a plain check passed over the installed service")
	}
	if result, err := f.run("", "check", "--upgrade", "--json"); err != nil {
		t.Fatal("an upgrade check of this installation failed:", result, err)
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

func TestSetupRejectsUntrustedExistingInputsBeforeChangingOwnership(t *testing.T) {
	for _, name := range []string{"program", "home", "config/settings.yaml", "scripts/untrusted.cmd"} {
		t.Run(name, func(t *testing.T) {
			f := newSetupCommandFixture(t)
			if err := os.MkdirAll(filepath.Join(f.home, "config"), 0o700); err != nil {
				t.Fatal(err)
			}
			settings := filepath.Join(f.home, "config", "settings.yaml")
			original := []byte("# keep existing input\nhttpListen: 127.0.0.1:7444\n")
			if err := os.WriteFile(settings, original, 0o600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.home, filepath.FromSlash(name))
			switch name {
			case "program":
				path = filepath.Dir(f.service.program)
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "home":
				path = f.home
			case "scripts/untrusted.cmd":
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("FAKE_UNTRUSTED_NOT_EXECUTED"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			policy := &setupObservedPaths{setupTestPaths: setupTestPaths{fail: path}}
			installer, err := winsvc.NewSetup(winsvc.SetupOptions{Service: f.service, Files: setupFileStore{Source: f.source, Paths: policy, Guard: setupGuardPath}, Pairer: f.service, Binding: func(context.Context) (winsvc.Definition, bool, error) {
				return winsvc.Definition{}, false, nil
			}, Lock: func(context.Context) (func(), error) { return func() {}, nil }, Now: time.Now, Source: f.source, Program: f.service.program, Version: "fake"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := installer.Install(context.Background(), winsvc.SetupRequest{Role: "both", Home: f.home, Listen: "127.0.0.1:7443", Confirmed: true}); err == nil || policy.protected != 0 || len(f.service.calls) != 0 {
				t.Fatal("untrusted input became protected SYSTEM configuration", err, policy.protected, f.service.calls)
			}
			if data, err := os.ReadFile(settings); err != nil || !bytes.Equal(data, original) {
				t.Fatal("rejected configuration was changed", err)
			}
		})
	}
}

func writeSetupMetadata(t *testing.T, directory, home string) {
	t.Helper()
	ini := "[Setup]\r\nProduct=lg-monitor\r\nHome=" + home + "\r\nRole=both\r\n"
	data, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(ini))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, winsvc.SetupMetadataFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSetupCLIUpdateResumesAfterTheOldServiceIsGone(t *testing.T) {
	f := newSetupCommandFixture(t)
	if result, err := f.run("", "install", "--confirm", "--json"); err != nil {
		t.Fatal(result, err)
	}
	// What an update that failed after removing the old service leaves: this installation's program, no service.
	if result, err := f.run("", "remove", "--confirm", "--json"); err != nil || f.service.installed {
		t.Fatal(result, err)
	}
	calls := len(f.service.calls)
	if _, err := f.run("", "check", "--json"); err == nil {
		t.Fatal("a plain install passed over the program left behind")
	}
	if result, err := f.run("", "check", "--upgrade", "--json"); err != nil {
		t.Fatal("retrying the update was refused:", result, err)
	}
	if err := os.Remove(filepath.Join(f.home, "data", "setup", "installation.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run("", "check", "--upgrade", "--json"); err == nil {
		t.Fatal("an update took over a program nothing records")
	}
	// The installer's setup.ini is written before the service is registered, so it also covers a failed first install.
	writeSetupMetadata(t, filepath.Dir(f.service.program), filepath.Join(filepath.Dir(f.home), "other-home"))
	if _, err := f.run("", "check", "--upgrade", "--json"); err == nil {
		t.Fatal("an update took over the program of another home")
	}
	writeSetupMetadata(t, filepath.Dir(f.service.program), f.home)
	if result, err := f.run("", "check", "--upgrade", "--json"); err != nil {
		t.Fatal("setup.ini of this home was not accepted:", result, err)
	}
	if len(f.service.calls) != calls || f.service.installed {
		t.Fatal("checking touched the service", f.service.calls)
	}
}

func TestSetupOwnedBelievesOnlyProtectedRecordsNamingThisHome(t *testing.T) {
	root := t.TempDir()
	// A long folder name, so that volumes making 8.3 names give it a short one that differs.
	home, program := filepath.Join(root, "home of this installation"), filepath.Join(root, "program", "lgmon.exe")
	receipt := filepath.Join(home, "data", "setup", "installation.json")
	for _, directory := range []string{filepath.Dir(receipt), filepath.Dir(program)} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(value winsvc.SetupReceipt) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(receipt, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	owned := func(paths setupTestPaths) bool {
		t.Helper()
		got, err := setupFileStore{Source: program, Paths: paths, Guard: setupGuardPath}.Owned(context.Background(), home, program)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	write(winsvc.SetupReceipt{Home: home, Program: program})
	if owned(setupTestPaths{}) {
		t.Fatal("a missing program was owned")
	}
	if err := os.WriteFile(program, []byte("fake executable bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !owned(setupTestPaths{}) {
		t.Fatal("this home's receipt was not believed")
	}
	if owned(setupTestPaths{fail: receipt}) {
		t.Fatal("an unprotected receipt was believed")
	}
	write(winsvc.SetupReceipt{Home: home, Program: filepath.Join(root, "elsewhere", "lgmon.exe")})
	if owned(setupTestPaths{}) {
		t.Fatal("a receipt for another program was believed")
	}
	write(winsvc.SetupReceipt{Home: filepath.Join(root, "other-home"), Program: program})
	if owned(setupTestPaths{}) {
		t.Fatal("a receipt for another home was believed")
	}
	writeSetupMetadata(t, filepath.Dir(program), strings.ToUpper(home))
	if !owned(setupTestPaths{}) {
		t.Fatal("setup.ini of this home was not believed")
	}
	if owned(setupTestPaths{fail: filepath.Join(filepath.Dir(program), winsvc.SetupMetadataFile)}) {
		t.Fatal("an unprotected setup.ini was believed")
	}
	// The installer records home as it was given and the setup command resolves it, so a short 8.3 spelling
	// (GitHub's runners have RUNNER~1 in their temporary folder) still names the same home.
	if short := setupShortPath(t, home); !strings.EqualFold(short, home) {
		writeSetupMetadata(t, filepath.Dir(program), short)
		if !owned(setupTestPaths{}) {
			t.Fatal("setup.ini naming this home by its short name was not believed:", short)
		}
	} else {
		t.Log("this volume makes no 8.3 names; the short spelling is checked where it does")
	}
}

func setupShortPath(t *testing.T, path string) string {
	t.Helper()
	long, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, windows.MAX_LONG_PATH)
	size, err := windows.GetShortPathName(long, &buffer[0], uint32(len(buffer)))
	if err != nil || size == 0 || int(size) > len(buffer) {
		return path
	}
	return windows.UTF16ToString(buffer[:size])
}
