package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/app"
	"github.com/LFenX/lg-monitor/internal/winsvc"
	"github.com/spf13/cobra"
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

func TestOpeningWaitsForAStartingServiceThenShowsItsWindow(t *testing.T) {
	f := &openFixture{alive: []bool{false, false, false, true}, status: openService{Installed: true, State: winsvc.StartPending, Home: `C:\fake-home`, Role: "both"}}
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

func TestUnknownCommandIsStillAUsageError(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := execute([]string{"no-such-command"}, &stdout, &stderr); code == 0 {
		t.Fatalf("unknown command exited 0: %s %s", stdout.String(), stderr.String())
	}
}
