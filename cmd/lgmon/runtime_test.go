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

	"github.com/LFenX/lg-monitor/internal/app"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/spf13/cobra"
)

func TestRunPassesHomeContextAndNotice(t *testing.T) {
	home := t.TempDir()
	ctx := context.WithValue(context.Background(), "test-context", "present")
	called := false
	command := newRunCommand(func(got context.Context, opts app.LocalOptions) error {
		called = true
		if got != ctx || opts.Home != home || opts.Version != version {
			t.Fatal("run did not preserve context, home or version")
		}
		return opts.Notice("运行 lgmon feishu setup 完成飞书接入")
	})
	command.PersistentFlags().String("home", home, "test home")
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"--role", "both"})
	if err := command.ExecuteContext(ctx); err != nil || !called || !strings.Contains(output.String(), "feishu setup") {
		t.Fatalf("run: %v %s", err, output.String())
	}
	command = newRunCommand(func(context.Context, app.LocalOptions) error { t.Fatal("invalid role reached runner"); return nil })
	command.SetArgs([]string{"--role", "invalid"})
	command.SilenceErrors, command.SilenceUsage = true, true
	var usage *usageError
	if err := command.Execute(); !errors.As(err, &usage) {
		t.Fatalf("invalid role: %v", err)
	}
}

func TestRunRoleAndTransportArguments(t *testing.T) {
	for _, role := range []string{"collector", "monitor"} {
		t.Run(role, func(t *testing.T) {
			called := false
			command := newRunCommand(func(_ context.Context, opts app.LocalOptions) error {
				called = true
				if opts.Role != role || opts.Listen != "127.0.0.1:17443" || opts.Monitor != "127.0.0.1:27443" {
					t.Fatalf("role options: %+v", opts)
				}
				return nil
			})
			command.PersistentFlags().String("home", t.TempDir(), "test home")
			command.SetArgs([]string{"--role", role, "--listen", "127.0.0.1:17443", "--monitor", "127.0.0.1:27443"})
			if err := command.Execute(); err != nil || !called {
				t.Fatalf("role: %v", err)
			}
		})
	}
}

func TestStatusAndAlertsReadStoppedDatabase(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(home, "data", "lgmon.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Write(ctx, pipeline.Message{CollectorID: "fake", Seq: 1, TS: 1000, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "demo", State: "down"}}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"firing", "resolved"} {
		alert := rules.Alert{ID: "fake-" + state, RuleID: "rule-" + state, CollectorID: "fake", TargetID: "demo", State: state, Severity: "warning", StartedAt: 1000}
		if err := db.SaveTransition(ctx, alert, rules.Event{AlertID: alert.ID, TS: 1000, Kind: state}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"status", "--json"}, {"alerts", "--json"}, {"alerts", "--json", "--all"}, {"status"}, {"alerts", "--all"}} {
		var stdout, stderr bytes.Buffer
		if code := execute(append([]string{"--home", home}, args...), &stdout, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("%v: %d %s", args, code, stderr.String())
		}
		if len(args) > 1 && args[1] == "--json" {
			var result struct {
				Available bool                 `json:"available"`
				Targets   []store.TargetRecord `json:"targets"`
				Alerts    []rules.Alert        `json:"alerts"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Available {
				t.Fatalf("%v: %s %v", args, stdout.String(), err)
			}
			want := 1
			if len(args) == 3 {
				want = 2
			}
			if len(result.Alerts) != want || (args[0] == "status" && len(result.Targets) != 1) {
				t.Fatalf("wrong read filters: %+v", result)
			}
		}
	}
	missing := filepath.Join(t.TempDir(), "not-created")
	for _, name := range []string{"status", "alerts"} {
		var stdout, stderr bytes.Buffer
		if code := execute([]string{"--home", missing, name, "--json"}, &stdout, &stderr); code != 0 || !json.Valid(stdout.Bytes()) || !bytes.Contains(stdout.Bytes(), []byte(`"available":false`)) || !bytes.Contains(stdout.Bytes(), []byte(`"alerts":[]`)) {
			t.Fatalf("missing %s: %d %s %s", name, code, stdout.String(), stderr.String())
		}
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("status or alerts created a missing home")
	}
}

type targetProcesses struct{ calls int }

func (p *targetProcesses) Snapshot(context.Context) ([]collector.Process, error) {
	p.calls++
	return []collector.Process{{PID: 123, Name: "demo.exe", CreatedAt: 1}}, nil
}

type targetPorts struct{ calls int }

func (p *targetPorts) ListeningTCP(context.Context) ([]collector.Port, error) {
	p.calls++
	return []collector.Port{{PID: 123, Port: 4567, LocalAddress: "127.0.0.1"}}, nil
}

func TestTargetsTestCollectsExactlyOnce(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "targets.yaml"), []byte("targets:\n- id: demo\n  match: {process: demo.exe, port: 4567}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	processes, ports := &targetProcesses{}, &targetPorts{}
	command := newTargetsCommand(func() (collector.ProcessSource, collector.PortSource) { return processes, ports })
	command.PersistentFlags().String("home", home, "test home")
	var stdout bytes.Buffer
	command.SetOut(&stdout)
	command.SetArgs([]string{"test", "demo", "--json"})
	if err := command.Execute(); err != nil || processes.calls != 1 || ports.calls != 1 {
		t.Fatalf("single collection: %v counts=%d/%d", err, processes.calls, ports.calls)
	}
	var result struct {
		TargetID string `json:"targetId"`
		State    string `json:"state"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.TargetID != "demo" || result.State != "up" || result.Reason == "" {
		t.Fatalf("target diagnosis: %s %v", stdout.String(), err)
	}
	if _, err := os.Stat(filepath.Join(home, "data")); !os.IsNotExist(err) {
		t.Fatal("single collection created a database")
	}
}

func TestCLIDocumentCoversAllLeafCommands(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "cli.md"))
	if err != nil {
		t.Fatal(err)
	}
	var visit func(*cobra.Command, string)
	visit = func(command *cobra.Command, path string) {
		if !command.HasSubCommands() && command.Name() != "help" {
			if !strings.Contains(string(doc), "lgmon "+path) {
				t.Errorf("CLI docs missing lgmon %s", path)
			}
			return
		}
		for _, child := range command.Commands() {
			visit(child, strings.TrimSpace(path+" "+child.Name()))
		}
	}
	visit(newRootCommand(), "")
}
