// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/update"
	"github.com/LFenX/lg-monitor/internal/winsvc"
)

type fakeRestartable struct {
	status    winsvc.Status
	restarted int
}

func (f *fakeRestartable) Install(context.Context, winsvc.Definition) error { return nil }
func (f *fakeRestartable) Uninstall(context.Context) error                  { return nil }
func (f *fakeRestartable) Start(context.Context) error                      { return nil }
func (f *fakeRestartable) Stop(context.Context) error                       { return nil }
func (f *fakeRestartable) Status(context.Context) (winsvc.Status, error)    { return f.status, nil }
func (f *fakeRestartable) Restart(context.Context) error                    { f.restarted++; return nil }

func fakeArchive(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(zipped)
	for name, body := range files {
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "lgmon-linux-amd64.tar.gz")
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runUpdate(t *testing.T, deps updateDependencies, args ...string) (updateResult, error) {
	t.Helper()
	command := newUpdateCommand(deps)
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(new(bytes.Buffer))
	command.SetIn(strings.NewReader(""))
	command.SetArgs(append(args, "--json"))
	err := command.Execute()
	var result updateResult
	if decodeErr := json.Unmarshal(out.Bytes(), &result); decodeErr != nil {
		t.Fatalf("output=%s err=%v", out.String(), decodeErr)
	}
	return result, err
}

func TestUpdateSwapsTheProgramAndRestartsTheRunningService(t *testing.T) {
	saved := version
	version = "0.17.5"
	t.Cleanup(func() { version = saved })
	dir := t.TempDir()
	program := filepath.Join(dir, "lgmon")
	if err := os.WriteFile(program, []byte("old program"), 0o755); err != nil {
		t.Fatal(err)
	}
	service := &fakeRestartable{status: winsvc.Status{Installed: true, State: winsvc.Running}}
	var asked string
	verified := ""
	deps := updateDependencies{
		GOOS: "linux", GOARCH: "amd64",
		Latest: func(_ context.Context, _, name string) (update.Release, error) {
			asked = name
			return update.Release{Version: "0.18.0", Page: "https://example.test/v0.18.0", File: name}, nil
		},
		Download: func(context.Context, update.Release, string) (string, error) {
			return fakeArchive(t, dir, map[string]string{"README.md": "x", "lgmon-0.18.0/lgmon": "new program"}), nil
		},
		Executable: func() (string, error) { return program, nil },
		Verify: func(_ context.Context, path string) error {
			data, _ := os.ReadFile(path)
			verified = string(data)
			return nil
		},
		Service: func() (serviceController, error) { return service, nil },
	}

	// --check only reports.
	result, err := runUpdate(t, deps, "--check")
	if err != nil || !result.Available || result.Updated || asked != "lgmon-linux-amd64.tar.gz" {
		t.Fatalf("check=%+v err=%v asked=%s", result, err, asked)
	}
	// Without a terminal or --yes nothing changes.
	if _, err := runUpdate(t, deps); err == nil {
		t.Fatal("an unconfirmed update went ahead")
	}
	result, err = runUpdate(t, deps, "--yes")
	if err != nil || !result.Updated || !result.Restarted || service.restarted != 1 || verified != "new program" {
		t.Fatalf("update=%+v err=%v", result, err)
	}
	if data, _ := os.ReadFile(program); string(data) != "new program" {
		t.Fatalf("program=%q", data)
	}
	if data, _ := os.ReadFile(program + ".previous"); string(data) != "old program" {
		t.Fatalf("previous=%q", data)
	}

	// A package without the program, or a program that does not run, leaves the old one in place.
	deps.Download = func(context.Context, update.Release, string) (string, error) {
		return fakeArchive(t, dir, map[string]string{"README.md": "x"}), nil
	}
	if _, err := runUpdate(t, deps, "--yes", "--force"); err == nil {
		t.Fatal("an archive without lgmon was accepted")
	}
	if data, _ := os.ReadFile(program); string(data) != "new program" {
		t.Fatalf("program changed: %q", data)
	}

	// Up to date: nothing to do; Windows points at its own installer.
	version = "0.18.0"
	if result, err := runUpdate(t, deps, "--yes"); err != nil || result.Available || result.Updated {
		t.Fatalf("up to date=%+v err=%v", result, err)
	}
	version = "0.17.5"
	deps.GOOS = "windows"
	if _, err := runUpdate(t, deps, "--yes"); err == nil || !strings.Contains(err.Error(), "小窗口") {
		t.Fatal(err)
	}
}
