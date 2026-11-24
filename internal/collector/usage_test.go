// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import (
	"context"
	"errors"
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
