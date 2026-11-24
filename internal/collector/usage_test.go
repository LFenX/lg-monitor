// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTopProcessesGroupsProgramsAndKeepsTheHeaviest(t *testing.T) {
	var processes []Process
	for i := range 30 {
		processes = append(processes, Process{PID: int32(100 + i), Name: "fake-worker-" + string(rune('a'+i)), RSS: uint64(1000 + i)})
	}
	processes = append(processes,
		Process{PID: 1, Name: "Browser.exe", RSS: 5_000_000, CPUPercent: 3},
		Process{PID: 2, Name: "browser.exe", RSS: 4_000_000, CPUPercent: 4},
		Process{PID: 3, Name: "busy", RSS: 10, CPUPercent: 60},
	)
	top := TopProcesses(processes)
	if len(top) > MaxTopProcesses || top[0].Name != "Browser.exe" || top[0].Count != 2 || top[0].PID != 0 || top[0].RSS != 9_000_000 || top[0].CPUPercent != 7 {
		t.Fatalf("top=%+v", top)
	}
	// A process light on memory but busy on CPU still makes the list.
	found := false
	for _, item := range top {
		if item.Name == "busy" && item.PID == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("the busy process is missing: %+v", top)
	}
}

func TestDockerStatsAreParsedFromBothUnitSystems(t *testing.T) {
	out := []byte(`{"BlockIO":"0B / 0B","CPUPerc":"7.20%","Container":"a1","ID":"a1b2c3d4e5f6a7b8","MemPerc":"30.80%","MemUsage":"4.8GiB / 15.6GiB","Name":"fake-read-model","NetIO":"1kB / 2kB","PIDs":"12"}
{"CPUPerc":"0.01%","ID":"ffff","MemPerc":"0.40%","MemUsage":"69.4MB / 1GB","Name":"/fake-reminders","PIDs":"3"}
`)
	items, err := ParseDockerStats(out)
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	first, gib := items[0], float64(1<<30)
	if first.Name != "fake-read-model" || first.ID != "a1b2c3d4e5f6" || first.CPUPercent != 7.2 || first.MemoryPercent != 30.8 || first.PIDs != 12 ||
		first.MemoryUsed != uint64(4.8*gib) || first.MemoryLimit != uint64(15.6*gib) {
		t.Fatalf("first=%+v", first)
	}
	if items[1].Name != "fake-reminders" || items[1].MemoryUsed != 69_400_000 {
		t.Fatalf("second=%+v", items[1])
	}
	if _, err := ParseDockerStats([]byte("not json")); err == nil {
		t.Fatal("garbage was accepted")
	}
}

func TestDockerCLIWithoutDockerIsNotAFailure(t *testing.T) {
	cli := &DockerCLI{Now: func() time.Time { return time.Unix(0, 0) }}
	cli.looked, cli.path = time.Unix(0, 0), ""
	if _, err := cli.Containers(context.Background()); !errors.Is(err, ErrNoContainerEngine) {
		t.Fatal(err)
	}
	cli.path = "fake-docker"
	cli.run = func(context.Context, string) ([]byte, error) {
		return []byte(`{"ID":"1","Name":"x","CPUPerc":"1%","MemPerc":"2%","MemUsage":"1MiB / 2MiB","PIDs":"1"}`), nil
	}
	if items, err := cli.Containers(context.Background()); err != nil || len(items) != 1 || items[0].MemoryLimit != 2<<20 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestSystemMetricsRejectInvalidDeviceFigures(t *testing.T) {
	base := SystemMetrics{TS: 1, CPUPercent: 10, MemoryTotal: 100, MemoryUsed: 50, MemoryAvailable: 50, MemoryPercent: 50}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*SystemMetrics){
		"swap over total":   func(m *SystemMetrics) { m.SwapTotal, m.SwapUsed = 1, 2 },
		"core over 100":     func(m *SystemMetrics) { m.CorePercent = []float64{101} },
		"nameless process":  func(m *SystemMetrics) { m.Top = []ProcessUsage{{Count: 1}} },
		"container percent": func(m *SystemMetrics) { m.Containers = []ContainerUsage{{Name: "x", MemoryPercent: 200}} },
		"negative load":     func(m *SystemMetrics) { m.Load1 = -1 },
	} {
		value := base
		change(&value)
		if value.Validate() == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestTopProcessesTellInterpretedProgramsApart(t *testing.T) {
	processes := []Process{
		{PID: 1, Name: "node", Cmdline: "node /srv/app/35-sync-mailbox-read-model.ts --port 9000", RSS: 300},
		{PID: 2, Name: "node", Cmdline: "/usr/bin/node /usr/lib/tsx/cli.mjs scripts/task-worker.ts", RSS: 200},
		{PID: 3, Name: "node", Cmdline: "node /srv/app/35-sync-mailbox-read-model.ts", RSS: 100},
		{PID: 4, Name: "python3", Cmdline: "python3 -m app.serve --token fake-secret", RSS: 50},
		{PID: 5, Name: "next-server (v", Exe: "/usr/bin/node", Cmdline: "next-server (v15.0.0)", RSS: 40},
		{PID: 6, Name: "containerd-shim", Exe: "/usr/bin/containerd-shim-runc-v2", RSS: 30},
		{PID: 7, Name: "postgres", Cmdline: "postgres: checkpointer", RSS: 20},
	}
	got := map[string]ProcessUsage{}
	for _, item := range TopProcesses(processes) {
		got[item.Name+"|"+item.Detail] = item
	}
	if item := got["node|35-sync-mailbox-read-model.ts"]; item.Count != 2 || item.RSS != 400 {
		t.Fatalf("read model=%+v all=%v", item, got)
	}
	if _, ok := got["node|cli.mjs"]; ok {
		t.Fatalf("the tsx launcher hid the script: %v", got)
	}
	if _, ok := got["python3|app.serve"]; !ok {
		t.Fatalf("python module missing: %v", got)
	}
	for key := range got {
		if strings.Contains(key, "fake-secret") {
			t.Fatalf("command line arguments leaked: %s", key)
		}
	}
	if _, ok := got["containerd-shim-runc-v2|"]; !ok {
		t.Fatalf("a name cut to 15 characters was not restored: %v", got)
	}
	if _, ok := got["next-server (v|"]; !ok {
		t.Fatalf("a cut name without a longer executable must stay as it is: %v", got)
	}
	if _, ok := got["postgres|"]; !ok {
		t.Fatalf("a program that is not an interpreter gained a detail: %v", got)
	}
}
