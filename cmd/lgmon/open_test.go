// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/app"
	"github.com/LFenX/lg-monitor/internal/winsvc"
	"github.com/spf13/cobra"
	"golang.org/x/text/encoding/unicode"
)

type openFixture struct {
	mu       sync.Mutex
	alive    []bool
	status   openService
	windows  []string
	notices  []string
	runs     []app.LocalOptions
	detached bool
}

func (f *openFixture) deps() openDependencies {
	return openDependencies{
		Panel: func(home string) (string, error) { return "http://127.0.0.1:7444", nil },
		Alive: func(context.Context, string) bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.alive) == 0 {
				return false
			}
			answer := f.alive[0]
			f.alive = f.alive[1:]
			return answer
		},
		Service: func(context.Context) (openService, error) { return f.status, nil },
		Window: func(_ context.Context, address string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.windows = append(f.windows, address)
			return nil
		},
		Run: func(_ context.Context, opts app.LocalOptions) error {
			f.runs = append(f.runs, opts)
			opts.HTTPReady("127.0.0.1:7444")
			opts.HTTPReady("127.0.0.1:7444")
			return nil
		},
		Notice: func(message string) { f.notices = append(f.notices, message) },
		Detach: func() { f.detached = true },
		Wait:   func(context.Context, time.Duration) error { return nil },
	}
}

func openCommand() *cobra.Command {
	command := &cobra.Command{}
	command.SetContext(context.Background())
	return command
}

func (f *openFixture) opened() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.windows...)
}

func TestOpeningARunningLgmonShowsItsWindow(t *testing.T) {
	f := &openFixture{alive: []bool{true}}
	if err := openLgmon(openCommand(), `C:\fake-home`, f.deps()); err != nil {
		t.Fatal(err)
	}
	if !f.detached || len(f.runs) != 0 || strings.Join(f.opened(), ",") != "http://127.0.0.1:7444" {
		t.Fatalf("detached=%v runs=%d windows=%v", f.detached, len(f.runs), f.opened())
	}
}

func TestDoubleClickUsesTheInstallersCustomHome(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "lgmon.exe")
	home := filepath.Join(directory, "监控数据")
	if got := installedLaunchHome(executable); got != "" {
		t.Fatalf("home without installer metadata = %q", got)
	}
	ini := "[Setup]\r\nProduct=lg-monitor\r\nHome=" + home + "\r\nRole=monitor\r\n"
	data, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(ini))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "setup.ini"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := installedLaunchHome(executable); got != home {
		t.Fatalf("installed home = %q, want %q", got, home)
	}
}

func TestOpeningWaitsForAStartingServiceThenShowsItsWindow(t *testing.T) {
	// Each look asks the panel's address and its three alternatives.
	f := &openFixture{alive: []bool{false, false, false, false, false, false, false, false, true}, status: openService{Installed: true, State: winsvc.StartPending, Home: `C:\fake-home`, Role: "both"}}
	if err := openLgmon(openCommand(), `C:\fake-home`, f.deps()); err != nil {
		t.Fatal(err)
	}
	if len(f.runs) != 0 || len(f.notices) != 0 || len(f.opened()) != 1 {
		t.Fatalf("runs=%d notices=%v windows=%v", len(f.runs), f.notices, f.opened())
	}
}

func TestOpeningAStoppedServiceExplainsInsteadOfStartingASecondLgmon(t *testing.T) {
	f := &openFixture{status: openService{Installed: true, State: winsvc.Stopped, Home: `C:\FAKE-HOME`, Role: "both"}}
	err := openLgmon(openCommand(), `C:\fake-home`, f.deps())
	if err == nil || len(f.runs) != 0 || len(f.opened()) != 0 || len(f.notices) != 1 || !strings.Contains(f.notices[0], "lgmon service start") {
		t.Fatalf("err=%v runs=%d windows=%v notices=%v", err, len(f.runs), f.opened(), f.notices)
	}
}

// Soon after a restart the delayed service is not up yet, so opening lgmon starts it.
func TestOpeningStartsAStoppedServiceAndShowsItsWindow(t *testing.T) {
	f := &openFixture{status: openService{Installed: true, State: winsvc.Stopped, Home: `C:\fake-home`, Role: "both"}}
	deps := f.deps()
	started := 0
	deps.Start = func(context.Context) error { started++; return nil }
	deps.Alive = func(_ context.Context, address string) bool {
		return started == 1 && address == "http://127.0.0.1:7444"
	}
	if err := openLgmon(openCommand(), `C:\fake-home`, deps); err != nil {
		t.Fatal(err)
	}
	if started != 1 || len(f.runs) != 0 || len(f.notices) != 0 || strings.Join(f.opened(), ",") != "http://127.0.0.1:7444" {
		t.Fatalf("started=%d notices=%v windows=%v", started, f.notices, f.opened())
	}
}

func TestDecliningToStartTheServiceSaysNothingMore(t *testing.T) {
	f := &openFixture{status: openService{Installed: true, State: winsvc.Stopped, Home: `C:\fake-home`, Role: "both"}}
	deps := f.deps()
	deps.Start = func(context.Context) error { return errStartDeclined }
	if err := openLgmon(openCommand(), `C:\fake-home`, deps); err != nil || len(f.notices) != 0 || len(f.opened()) != 0 || len(f.runs) != 0 {
		t.Fatalf("err=%v notices=%v", err, f.notices)
	}
}

func TestAServiceThatKeepsFailingSaysWhyWithoutALongWait(t *testing.T) {
	f := &openFixture{status: openService{Installed: true, State: winsvc.Stopped, Home: `C:\fake-home`, Role: "both"}}
	deps := f.deps()
	waits := 0
	deps.Wait = func(context.Context, time.Duration) error { waits++; return nil }
	deps.Start = func(context.Context) error { return nil }
	deps.Failure = func(context.Context) string { return "监听采集连接失败：fake reason" }
	err := openLgmon(openCommand(), `C:\fake-home`, deps)
	if err == nil || len(f.notices) != 1 || !strings.Contains(f.notices[0], "fake reason") || waits >= openAttempts/2 {
		t.Fatalf("err=%v waits=%d notices=%v", err, waits, f.notices)
	}
}

// When Windows reserved the usual port, lgmon answers at one of its fixed alternatives.
func TestOpeningFindsLgmonAtItsAlternativePort(t *testing.T) {
	f := &openFixture{}
	deps := f.deps()
	var asked []string
	deps.Alive = func(_ context.Context, address string) bool {
		asked = append(asked, address)
		return address == "http://127.0.0.1:27444"
	}
	if err := openLgmon(openCommand(), `C:\fake-home`, deps); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.opened(), ",") != "http://127.0.0.1:27444" || len(asked) != 3 || asked[0] != "http://127.0.0.1:7444" {
		t.Fatalf("asked %v, opened %v", asked, f.opened())
	}
}

func TestOpeningACollectorOnlyServiceSaysWhereTheBoardIs(t *testing.T) {
	f := &openFixture{status: openService{Installed: true, State: winsvc.Running, Home: `C:\fake-home`, Role: "collector"}}
	err := openLgmon(openCommand(), `C:\fake-home`, f.deps())
	if err == nil || len(f.runs) != 0 || len(f.notices) != 1 || !strings.Contains(f.notices[0], "只负责采集") {
		t.Fatalf("err=%v runs=%d notices=%v", err, len(f.runs), f.notices)
	}
}

func TestAServiceForAnotherHomeDoesNotStopOpeningThisOne(t *testing.T) {
	f := &openFixture{status: openService{Installed: true, State: winsvc.Stopped, Home: `C:\ProgramData\lg-monitor`, Role: "both"}}
	if err := openLgmon(openCommand(), `C:\fake-home`, f.deps()); err != nil {
		t.Fatal(err)
	}
	if len(f.notices) != 0 || len(f.runs) != 1 || f.runs[0].Home != `C:\fake-home` {
		t.Fatalf("notices=%v runs=%+v", f.notices, f.runs)
	}
}

func TestOpeningWithNothingRunningStartsLgmonAndShowsTheWindowOnce(t *testing.T) {
	f := &openFixture{status: openService{}}
	if err := openLgmon(openCommand(), `C:\fake-home`, f.deps()); err != nil {
		t.Fatal(err)
	}
	if len(f.runs) != 1 || f.runs[0].Role != "both" || f.runs[0].Listen != "127.0.0.1:7443" || f.runs[0].Home != `C:\fake-home` || f.runs[0].NoTray {
		t.Fatalf("run options: %+v", f.runs)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(f.opened()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if strings.Join(f.opened(), ",") != "http://127.0.0.1:7444" {
		t.Fatalf("windows=%v", f.opened())
	}
}

func TestAnInstalledCopyWithoutItsServiceSendsToTheInstallerInsteadOfRunningItself(t *testing.T) {
	// What happened on a colleague's computer: the service was gone after a failed update, opening lgmon from
	// the Start menu ran it for that person, and every later update failed to start the service it registered.
	f := &openFixture{status: openService{}}
	deps := f.deps()
	deps.Installed = func() string { return `C:\FAKE-HOME` }
	err := openLgmon(openCommand(), `C:\fake-home`, deps)
	if err == nil || len(f.runs) != 0 || len(f.notices) != 1 || !strings.Contains(f.notices[0], "继续安装") {
		t.Fatalf("err=%v runs=%d notices=%v", err, len(f.runs), f.notices)
	}
	// A copy installed for another home, or one nobody installed, still runs for this one.
	for _, installed := range []string{`C:\ProgramData\lg-monitor`, ""} {
		f := &openFixture{status: openService{}}
		deps := f.deps()
		deps.Installed = func() string { return installed }
		if err := openLgmon(openCommand(), `C:\fake-home`, deps); err != nil || len(f.runs) != 1 || len(f.notices) != 0 {
			t.Fatalf("installed=%q err=%v runs=%d notices=%v", installed, err, len(f.runs), f.notices)
		}
	}
}

func TestUnknownCommandIsStillAUsageError(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := execute([]string{"no-such-command"}, &stdout, &stderr); code == 0 {
		t.Fatalf("unknown command exited 0: %s %s", stdout.String(), stderr.String())
	}
}

// lockedConfig answers like the service's home does for a person without elevation: access denied.
type lockedConfig struct{}

func (lockedConfig) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
}

func TestThePanelAddressOfAHomeThisPersonCannotReadIsTheDefault(t *testing.T) {
	defaultAddress, err := app.TrayMonitorURL("127.0.0.1:7444")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := panelAddress(lockedConfig{}); err != nil || got != defaultAddress {
		t.Fatalf("locked settings: %q %v, want the default %q so the window can check whether lgmon answers there", got, err, defaultAddress)
	}
	if got, err := panelAddress(fstest.MapFS{}); err != nil || got != defaultAddress {
		t.Fatalf("no settings: %q %v", got, err)
	}
	custom := fstest.MapFS{"settings.yaml": {Data: []byte("httpListen: 127.0.0.1:9444\n")}}
	want, _ := app.TrayMonitorURL("127.0.0.1:9444")
	if got, err := panelAddress(custom); err != nil || got != want {
		t.Fatalf("readable settings: %q %v, want %q", got, err, want)
	}
	broken := fstest.MapFS{"settings.yaml": {Data: []byte("httpListen: [\n")}}
	if got, err := panelAddress(broken); err == nil {
		t.Fatalf("a broken settings file must still be reported, got %q", got)
	}
}

func TestReachablePanelMovesOnlyWhenNothingListens(t *testing.T) {
	ctx := context.Background()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests++ }))
	defer server.Close()
	if got := reachablePanel(ctx, server.URL); got != server.URL || requests != 0 {
		t.Fatalf("a working address must be used as it is: %s after %d requests", got, requests)
	}
	for _, address := range []string{"https://127.0.0.1:1", "http://192.0.2.1:7444", "http://monitor.example:7444", "::"} {
		if got := reachablePanel(ctx, address); got != address {
			t.Fatalf("%s became %s", address, got)
		}
	}
}
