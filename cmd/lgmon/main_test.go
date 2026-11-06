package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionWithPersistentFlags(t *testing.T) {
	logger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(logger) })
	for _, args := range [][]string{
		{"--home", t.TempDir(), "--log-level", "debug", "version"},
		{"version", "--home", t.TempDir(), "--log-level", "warn"},
	} {
		root := newRootCommand()
		var output bytes.Buffer
		root.SetOut(&output)
		root.SetErr(&output)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("Execute(%v): %v", args, err)
		}
		want := fmt.Sprintf("版本：%s\n提交：%s\n构建时间：%s\n", version, commit, buildTime)
		if output.String() != want {
			t.Fatalf("output = %q, want %q", output.String(), want)
		}
	}
}

func TestHomePrecedence(t *testing.T) {
	programData := t.TempDir()
	environmentHome := t.TempDir()
	flagHome := t.TempDir()
	t.Setenv("ProgramData", programData)
	t.Setenv("LGMON_HOME", "")
	if got, want := defaultHome(), filepath.Join(programData, "lg-monitor"); got != want {
		t.Fatalf("defaultHome() = %q, want %q", got, want)
	}
	t.Setenv("LGMON_HOME", environmentHome)
	root := newRootCommand()
	if got := root.PersistentFlags().Lookup("home").DefValue; got != environmentHome {
		t.Fatalf("home default = %q, want %q", got, environmentHome)
	}
	if err := root.ParseFlags([]string{"--home", flagHome}); err != nil {
		t.Fatal(err)
	}
	if got, err := root.PersistentFlags().GetString("home"); err != nil || got != flagHome {
		t.Fatalf("home = %q, error = %v, want %q", got, err, flagHome)
	}
}

func TestInvalidLogLevel(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"version", "--log-level", "invalid"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "日志级别") {
		t.Fatalf("error = %v, want a Chinese log level error", err)
	}
	if output.Len() != 0 {
		t.Fatalf("invalid command produced output: %q", output.String())
	}
}

func TestVersionRejectsArguments(t *testing.T) {
	root := newRootCommand()
	root.SetArgs([]string{"version", "extra"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected an error for unexpected arguments")
	}
}
