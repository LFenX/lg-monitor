package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestPairNamedMonitorDoesNotOverwriteDefaultJoin(t *testing.T) {
	home := t.TempDir()
	called := 0
	deps := pairDependencies{
		Join: func(context.Context, string, string, string, string, func(string) (bool, error)) (string, error) {
			t.Fatal("named join reached default identity")
			return "", nil
		},
		JoinMonitor: func(_ context.Context, got, name, address, code, pin string, confirm func(string) (bool, error)) (string, error) {
			called++
			if got != home || name != "company" || address != "monitor.test:7443" || code != "fake-pair-code" || pin != strings.Repeat("a", 64) || confirm != nil {
				t.Fatal("named join arguments")
			}
			return pin, nil
		},
	}
	run := func(name string) (string, error) {
		root := &cobra.Command{Use: "test", SilenceErrors: true, SilenceUsage: true}
		root.PersistentFlags().String("home", home, "")
		root.AddCommand(newPairCommand(deps))
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetArgs([]string{"pair", "join", "--name", name, "--monitor", "monitor.test:7443", "--code", "fake-pair-code", "--fingerprint", strings.Repeat("a", 64), "--json"})
		err := root.Execute()
		return out.String(), err
	}
	output, err := run("company")
	if err != nil || !strings.Contains(output, "collectorMonitors") || strings.Contains(output, "fake-pair-code") || called != 1 {
		t.Fatal(output, err, called)
	}
	if _, err := run("../default"); err == nil || called != 1 {
		t.Fatal("unsafe name reached pairing")
	}
}

func TestPairMonitorStatusIncludesDisabledCapacityAndIdentityErrors(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "settings.yaml"), []byte("cache: {maxSize: 64MB}\ncollectorMonitors:\n- {id: company}\n- {id: paused, enabled: false}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root := &cobra.Command{Use: "test", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("home", home, "")
	root.AddCommand(newPairCommand(pairDependencies{}))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"pair", "monitors", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Monitors                                              []monitorStatus
		MaxBytesPerMonitor, TotalMaxBytes, TotalPhysicalBytes int64
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Monitors) != 3 || result.TotalMaxBytes != 128_000_000 || result.MaxBytesPerMonitor != 64_000_000 || result.TotalPhysicalBytes != 0 || result.Monitors[2].Enabled || result.Monitors[1].AllowOperations {
		t.Fatalf("unsafe status/capacity: %+v", result)
	}
	for _, item := range result.Monitors {
		if item.IdentityError == "" || item.Cache != nil {
			t.Fatal(item)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "data", "collector-id")); !os.IsNotExist(err) {
		t.Fatal("status generated a collector identity", err)
	}
}
