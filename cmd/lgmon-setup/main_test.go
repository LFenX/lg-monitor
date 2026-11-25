//go:build windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/unicode"
)

func TestTheEngineGetsExactlyTheChoice(t *testing.T) {
	args := engineArgs(choice{Role: "both", Home: `C:\fake home\中文`, Listen: "127.0.0.1:7443"}, `C:\fake\install.log`)
	for _, want := range []string{"/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/LGMONROLE=both", "/LGMONCONFIRM", `/LOG=C:\fake\install.log`, `/LGMONHOME=C:\fake home\中文`, "/LGMONLISTEN=127.0.0.1:7443"} {
		if !slices.Contains(args, want) {
			t.Fatalf("missing %s in %v", want, args)
		}
	}
	if slices.Contains(args, "/LGMONUPGRADE") {
		t.Fatal("a fresh install asked for an upgrade")
	}
	update := engineArgs(choice{Role: "collector", Home: `C:\fake`, Listen: "0.0.0.0:7443", Monitor: "fake-monitor:7443", Upgrade: true}, "x.log")
	if !slices.Contains(update, "/LGMONUPGRADE") || !slices.Contains(update, "/LGMONMONITOR=fake-monitor:7443") || slices.ContainsFunc(update, func(arg string) bool { return strings.HasPrefix(arg, "/LGMONLISTEN") }) {
		t.Fatalf("collector update args %v", update)
	}
	if defaults := engineArgs(choice{}, "x.log"); !slices.Contains(defaults, "/LGMONROLE=both") {
		t.Fatalf("default role %v", defaults)
	}
}

func TestProgressFollowsTheEngineLog(t *testing.T) {
	log := strings.Join([]string{
		"2026-11-17 10:00:00.000   Setup version: Inno Setup version 6.7.4",
		"2026-11-17 10:00:01.000   lgmon-stage: check",
	}, "\r\n")
	if got := progress(log); got.Percent != 16 || !strings.Contains(got.Step, "检查") {
		t.Fatalf("check stage %+v", got)
	}
	// An update stops the old version only after every check has passed.
	if got := progress(log + "\r\n2026-11-17 10:00:01.500   lgmon-stage: upgrade"); got.Percent != 22 || !strings.Contains(got.Step, "旧版本") {
		t.Fatalf("upgrade stage %+v", got)
	}
	log += "\r\n2026-11-17 10:00:02.000   Starting the installation process.\r\n2026-11-17 10:00:02.100   -- File entry --\r\n2026-11-17 10:00:02.100   Dest filename: C:\\fake\\lgmon.exe\r\n2026-11-17 10:00:02.200   Dest filename: C:\\fake\\lgmon-window.exe"
	if got := progress(log); got.Percent != 42 {
		t.Fatalf("files %+v", got)
	}
	log += "\r\n2026-11-17 10:00:03.000   lgmon-stage: service"
	if got := progress(log); got.Percent != 60 || !strings.Contains(got.Step, "服务") {
		t.Fatalf("service %+v", got)
	}
	if got := progress(log + "\r\n2026-11-17 10:00:09.000   Installation process succeeded."); got.Percent != 96 {
		t.Fatalf("succeeded %+v", got)
	}
	if got := progress(""); got.Percent != 3 {
		t.Fatalf("no log yet %+v", got)
	}
}

func TestAFailureSaysWhyInTheEnginesWords(t *testing.T) {
	log := "2026-11-17 10:00:01.000   Exception message:\r\n2026-11-17 10:00:01.000   Message box (OK):\r\n2026-11-17 10:00:01.000   lgmon 服务已存在，没有覆盖；请备份 home"
	if got := failure(log+"\r\n2026-11-17 10:00:01.000   PrepareToInstall failed: 服务程序目录固定，不能通过 /DIR 改到其他位置。", 7); got != "服务程序目录固定，不能通过 /DIR 改到其他位置。" {
		t.Fatalf("reason %q", got)
	}
	if got := failure("", 5); !strings.Contains(got, "取消") {
		t.Fatalf("cancel %q", got)
	}
	if got := failure("", 20); !strings.Contains(got, "20") || !strings.Contains(got, "数据") {
		t.Fatalf("plain %q", got)
	}
}

func TestOptions(t *testing.T) {
	opts, err := parseOptions([]string{"--silent", "--update", "--engine", `C:\fake\engine.exe`, "--role", "monitor", "--home", `C:\fake`}, io.Discard)
	if err != nil || !opts.silent || !opts.update || !opts.choice.Upgrade || opts.engine != `C:\fake\engine.exe` || opts.choice.Role != "monitor" {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
	if _, err := parseOptions([]string{"--role", "everything"}, io.Discard); err == nil {
		t.Fatal("accepted an unknown role")
	}
	if !strings.Contains(page, "lgmon-host") || !strings.Contains(page, "postMessage") {
		t.Fatal("the page does not talk to its window")
	}
}

func TestAProgramWithoutItsServiceIsOfferedAsAnUpdateWithWhatSetupIniRecords(t *testing.T) {
	directory := t.TempDir()
	if found := leftInstall(directory); found != nil {
		t.Fatal("an empty program directory was taken for an installation", found)
	}
	if err := os.WriteFile(filepath.Join(directory, "lgmon.exe"), []byte("fake executable bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if found := leftInstall(directory); found == nil || !found.Incomplete || found.Home != "" {
		t.Fatal("a program without setup.ini", found)
	}
	home := filepath.Join(directory, "监控数据")
	ini := "[Setup]\r\nProduct=lg-monitor\r\nHome=" + home + "\r\nRole=collector\r\nMonitor=fake-monitor:7443\r\n"
	data, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(ini))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "setup.ini"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	found := leftInstall(directory)
	if found == nil || !found.Incomplete || found.Home != home || found.Role != "collector" || found.Monitor != "fake-monitor:7443" {
		t.Fatalf("left installation = %+v", found)
	}
	// The update then reinstalls exactly what was there.
	c := keep(choice{Upgrade: true}, found)
	if c.Home != home || c.Role != "collector" || c.Monitor != "fake-monitor:7443" || !c.Upgrade {
		t.Fatalf("update choice = %+v", c)
	}
}

func TestEveryAttemptKeepsItsOwnLog(t *testing.T) {
	directory := t.TempDir()
	start := time.Date(2026, 11, 20, 1, 0, 0, 0, time.UTC)
	var made []string
	for attempt := 0; attempt < 5; attempt++ {
		log := attemptLog(directory, start.Add(time.Duration(attempt)*time.Second), 3)
		if err := os.WriteFile(log, []byte("attempt"), 0o600); err != nil {
			t.Fatal(err)
		}
		made = append(made, log)
	}
	if len(slices.Compact(slices.Clone(made))) != len(made) {
		t.Fatal("two attempts shared a log", made)
	}
	left, err := filepath.Glob(filepath.Join(directory, "install-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(left, made[2:]) {
		t.Fatalf("kept %v, want the newest three %v", left, made[2:])
	}
}
func TestReceiptOnlyFromDistributionInstaller(t *testing.T) {
	id := strings.Repeat("ab", 16)
	for _, name := range []string{"lgmon-setup-0.16.0--" + id + ".exe", "lgmon-setup-0.16.0--" + id + " (2).exe"} {
		if got := receiptArgs(name, []string{"/LOG"}); !slices.Equal(got, []string{"/LOG", "/LGMONRECEIPT=" + id}) {
			t.Fatalf("receipt args = %v", got)
		}
	}
	if got := receiptArgs("lgmon-setup-0.16.0.exe", nil); len(got) != 0 {
		t.Fatalf("ordinary installer received args %v", got)
	}
}

func TestOnlyAnIdleOlderInstallerIsReplaced(t *testing.T) {
	for _, test := range []struct {
		version, path string
		installing    bool
		want          bool
	}{
		{"0.22.0", `lgmon-setup-0.21.0.exe`, false, true},
		{"0.22.0", `LGMON-Setup-0.9.0 (1).exe`, false, true},
		{"0.22.0", `lgmon-setup-0.21.0--0123456789abcdef0123456789abcdef (2).exe`, false, true},
		{"0.22.0", `lgmon-setup-0.21.0.exe`, true, false},
		{"0.22.0", `lgmon-setup-0.22.0.exe`, false, false},
		{"0.21.0", `lgmon-setup-0.22.0.exe`, false, false},
		{"0.22.0", `other.exe`, false, false},
		{"0.22.0", "", false, false},
	} {
		if got := supersedes(test.version, test.path, test.installing); got != test.want {
			t.Errorf("%+v: got %v", test, got)
		}
	}
}
