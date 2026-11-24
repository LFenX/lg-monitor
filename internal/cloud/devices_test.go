// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package cloud

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
)

type deviceProvider struct {
	*fakeProvider
	cpu float64
}

func (p *deviceProvider) CloudDevices(context.Context) ([]api.Device, error) {
	top := make([]collector.ProcessUsage, 30)
	for index := range top {
		top[index] = collector.ProcessUsage{Name: "fake", CPUPercent: 1.234}
	}
	return []api.Device{
		{ID: "local", Hostname: "fake-host", Online: true, System: &collector.SystemMetrics{TS: 1, CPUPercent: p.cpu, CorePercent: []float64{12.6, 40.2}, Errors: []string{"local only"}}, Usage: &api.DeviceUsage{Top: top, ContainerError: "local only"}},
		{ID: "pending", Hostname: "no-sample"},
	}, nil
}

func TestDeviceResourcesRideTheSlowStateRefresh(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }
	var pushes []SyncPushRequest
	client := &scriptedClient{handle: func(call scriptedCall) (int, any, string) {
		if status, body, etag, ok := signedInReply(call); ok {
			return status, body, etag
		}
		if call.Path == "/v1/sync/push" {
			var req SyncPushRequest
			_ = json.Unmarshal(call.Body, &req)
			pushes = append(pushes, req)
			return 200, SyncPushResponse{OK: true}, ""
		}
		return 404, nil, ""
	}}
	provider := &deviceProvider{fakeProvider: oneMonitor(10), cpu: 23.456}
	mgr := newSignedIn(t, t.TempDir(), &fakeSecrets{}, client, clock, provider)
	ctx := context.Background()
	if err := mgr.PushOnce(ctx, false); err != nil || len(pushes) != 1 || pushes[0].State == nil {
		t.Fatalf("first push: %+v %v", pushes, err)
	}
	devices := pushes[0].State.Devices
	if len(devices) != 1 || devices[0].System.CPUPercent != 23.5 || devices[0].System.CorePercent[0] != 13 || devices[0].System.Errors != nil {
		t.Fatalf("devices=%+v", devices)
	}
	if usage := devices[0].Usage; len(usage.Top) != maxSyncedPrograms || usage.Top[0].CPUPercent != 1.2 || usage.ContainerError != "" {
		t.Fatalf("usage=%+v", usage)
	}
	// A busier CPU alone waits for the next slow refresh instead of sending the state every minute.
	provider.cpu = 80
	now = now.Add(61 * time.Second)
	if err := mgr.PushOnce(ctx, false); err != nil || len(pushes) != 2 || pushes[1].State != nil {
		t.Fatalf("sample change pushed early: %+v %v", pushes, err)
	}
	now = now.Add(stateRefreshEvery)
	if err := mgr.PushOnce(ctx, false); err != nil || len(pushes) != 3 || pushes[2].State == nil || pushes[2].State.Devices[0].System.CPUPercent != 80 {
		t.Fatalf("refreshed sample missing: %+v %v", pushes, err)
	}

	// The receiving device reads the sample back for the other device's device page.
	payload := RemoteDevicePayload{ID: "dev-home", Name: "Home", Online: true, State: pushes[2].State}
	mgr.mu.Lock()
	mgr.rawDevices["dev-home"], mgr.devices["dev-home"] = payload, ReconstructDevice(payload)
	mgr.mu.Unlock()
	system, err := mgr.DeviceSystem("dev-home")
	if err != nil || system.DeviceName != "Home" || len(system.Devices) != 1 || system.Devices[0].Hostname != "fake-host" {
		t.Fatalf("system=%+v %v", system, err)
	}
	if _, err := mgr.DeviceSystem("dev-missing"); err == nil {
		t.Fatal("unknown device accepted")
	}
}
