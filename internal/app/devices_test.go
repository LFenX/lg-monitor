// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestDevicesShowTheLatestSampleAndTheLatestUsage(t *testing.T) {
	f := newAPIFixture(t)
	ts := f.now.UnixMilli()
	usage := &collector.SystemMetrics{TS: ts - 20000, CPUPercent: 30, MemoryTotal: 100, MemoryUsed: 90, MemoryAvailable: 10, MemoryPercent: 90, ProcessCount: 321,
		Top:        []collector.ProcessUsage{{Name: "fake-db", Count: 3, CPUPercent: 2, RSS: 40}},
		Containers: []collector.ContainerUsage{{ID: "abc", Name: "fake-web", CPUPercent: 1, MemoryUsed: 30, MemoryLimit: 60, MemoryPercent: 50, PIDs: 4}}}
	latest := &collector.SystemMetrics{TS: ts, CPUPercent: 12, CPUCores: 2, CorePercent: []float64{10, 14}, Load1: 0.5, MemoryTotal: 100, MemoryUsed: 91, MemoryAvailable: 9, MemoryPercent: 91, SwapTotal: 50, SwapUsed: 10, SwapPercent: 20}
	for i, sample := range []*collector.SystemMetrics{usage, latest} {
		if err := f.db.Write(context.Background(), pipeline.Message{CollectorID: "fake-host", Seq: uint64(900 + i), TS: sample.TS, Kind: pipeline.KindSystemMetrics, SystemMetrics: sample}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	devices := decodeAs[[]api.Device](t, f.request(t, "GET", "/api/v1/devices", "read", "", 200).Body.Bytes())
	var host api.Device
	for _, device := range devices {
		if device.ID == "fake-host" {
			host = device
		}
	}
	if host.System == nil || host.System.MemoryPercent != 91 || len(host.System.CorePercent) != 2 || host.System.SwapPercent != 20 || host.System.Top != nil {
		t.Fatalf("system=%+v", host.System)
	}
	if host.Usage == nil || host.Usage.ProcessCount != 321 || len(host.Usage.Top) != 1 || host.Usage.Top[0].Name != "fake-db" || len(host.Usage.Containers) != 1 || host.Usage.Containers[0].Name != "fake-web" {
		t.Fatalf("usage=%+v", host.Usage)
	}
	for _, device := range devices {
		if device.ID == "fake-pending" && (device.System != nil || device.Online) {
			t.Fatalf("a computer without samples=%+v", device)
		}
	}
	// Guests on the LAN do not see the computers.
	f.request(t, "GET", "/api/v1/devices", "", "", 401)
}

func TestDeviceAlertsSwitchOnlyTheirOwnRules(t *testing.T) {
	f := newAPIFixture(t)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构网站","id":"site","port":3000}`, 200)
	before := len(f.manager.Snapshot().Rules)
	alerts := decodeAs[api.DeviceAlerts](t, f.request(t, "GET", "/api/v1/devices/alerts", "read", "", 200).Body.Bytes())
	if alerts.Memory.Enabled || alerts.Memory.Threshold != 92 || alerts.Memory.For != "2m" {
		t.Fatalf("defaults=%+v", alerts)
	}
	f.request(t, "PUT", "/api/v1/devices/alerts", "read", `{"memory":{"enabled":true,"threshold":90,"for":"2m"}}`, 403)
	for body, want := range map[string]string{
		`{"memory":{"enabled":true,"threshold":0,"for":"2m"}}`:    "1～100",
		`{"memory":{"enabled":true,"threshold":90,"for":"soon"}}`: "持续时间",
	} {
		if response := f.request(t, "PUT", "/api/v1/devices/alerts", "config", body, 400); !strings.Contains(response.Body.String(), want) {
			t.Fatalf("%s: %s", body, response.Body.String())
		}
	}
	saved := decodeAs[api.DeviceAlerts](t, f.request(t, "PUT", "/api/v1/devices/alerts", "config",
		`{"memory":{"enabled":true,"threshold":95,"for":"1m"},"containerMemory":{"enabled":true,"threshold":88,"for":"30s"}}`, 200).Body.Bytes())
	if !saved.Memory.Enabled || saved.Memory.Threshold != 95 || saved.Memory.For != "1m" || !saved.ContainerMemory.Enabled || saved.CPU.Enabled || len(saved.Notify) == 0 {
		t.Fatalf("saved=%+v", saved)
	}
	rules := f.manager.Snapshot().Rules
	if len(rules) != before+2 {
		t.Fatalf("rules=%d before=%d", len(rules), before)
	}
	var memory config.Rule
	for _, rule := range rules {
		if rule.ID == "device-memory" {
			memory = rule
		}
	}
	if memory.Type != "metric_threshold" || memory.Metric != "system.memory_percent" || memory.Op != "gte" || *memory.Threshold != 95 || memory.Target != "" || memory.Severity != "critical" {
		t.Fatalf("memory rule=%+v", memory)
	}
	// Switching one off removes only that rule; the service's own rules stay.
	f.request(t, "PUT", "/api/v1/devices/alerts", "config", `{"containerMemory":{"enabled":true,"threshold":88,"for":"30s"}}`, 200)
	if rules := f.manager.Snapshot().Rules; len(rules) != before+1 {
		t.Fatalf("after switching memory off: %d rules", len(rules))
	}
}
