package main

import (
	"io"
	"slices"
	"strings"
	"testing"
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
