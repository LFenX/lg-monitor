//go:build windows && integration && webui

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/store"
)

type nativeOperationPaths struct{}

func (nativeOperationPaths) Check(string) error             { return nil }
func (nativeOperationPaths) ProtectTree(string, bool) error { return nil }

type nativeOperationExecutor struct {
	calls            atomic.Int32
	entered, release chan struct{}
}

func (e *nativeOperationExecutor) Restart(ctx context.Context, name string) error {
	if name != "FakeService" {
		return fmt.Errorf("fixture received non-owned service")
	}
	e.calls.Add(1)
	close(e.entered)
	select {
	case <-ctx.Done():
		return collector.ErrRestartUncertain
	case <-e.release:
		return nil
	}
}

func TestNativeServiceOperationMTLSDefaultsHotPolicyAndPersistentNoRepeat(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	home := t.TempDir()
	directory := filepath.Join(home, "config")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	settings := config.Settings{HTTPListen: "127.0.0.1:0", RemoteOperations: config.RemoteOperations{Services: []config.ServiceGrant{{ID: "restart-demo", Service: "FakeService"}}}}
	writeSettings := func() {
		t.Helper()
		data, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "settings.yaml"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeSettings()
	auth, err := store.OpenAuthDB(ctx, filepath.Join(home, "data", "auth.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"read", "config", "admin"} {
		hash := api.TokenHash("fake-operation-" + role)
		if _, err := auth.AddAPIToken(ctx, hash[:], "fake-"+role, role); err != nil {
			auth.Close()
			t.Fatal(err)
		}
	}
	if err := auth.Close(); err != nil {
		t.Fatal(err)
	}
	executor := &nativeOperationExecutor{entered: make(chan struct{}), release: make(chan struct{})}
	ready := make(chan string, 2)
	done := make(chan error, 1)
	go func() {
		done <- RunLocal(ctx, LocalOptions{Home: home, Role: "both", Listen: "127.0.0.1:0", ServiceIdentity: "LocalSystem", Paths: nativeOperationPaths{}, ServiceRestarter: executor, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), HTTPReady: func(address string) { ready <- address }})
	}()
	t.Cleanup(func() {
		select {
		case <-executor.release:
		default:
			close(executor.release)
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(8 * time.Second):
			t.Error("owned monitor/collector did not stop")
		}
	})
	var address string
	select {
	case address = <-ready:
	case <-ctx.Done():
		t.Fatal("HTTP fixture did not start")
	}
	client := func(role string) *api.MCPHTTPClient {
		t.Helper()
		value, err := api.NewMCPHTTPClient(api.MCPHTTPClientOptions{BaseURL: "http://" + address, Token: "fake-operation-" + role})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	admin, read, limited := client("admin"), client("read"), client("config")
	var id string
	deadline := time.Now().Add(8 * time.Second)
	for id == "" && time.Now().Before(deadline) {
		response, err := read.Call(ctx, api.ListCollectors, api.Request{})
		if err == nil && response.Status == 200 {
			var rows []struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(response.Body, &rows) == nil && len(rows) > 0 {
				id = rows[0].ID
			}
		}
		if id == "" {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if id == "" {
		t.Fatal("self-paired collector did not connect")
	}
	params := map[string]string{"id": id}
	var response api.MCPResponse
	deadline = time.Now().Add(8 * time.Second)
	for {
		response, err = read.Call(ctx, api.ListServiceOperations, api.Request{Params: params})
		if err == nil && response.Status == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("collector identity existed but authenticated operation stream did not become ready", response, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || response.Status != 200 {
		t.Fatal(response, err)
	}
	var policy collector.OperationPolicy
	if err := json.Unmarshal(response.Body, &policy); err != nil || policy.Enabled {
		t.Fatal(policy, err)
	}
	if _, err := os.Stat(filepath.Join(home, "data", "operations.db")); !os.IsNotExist(err) {
		t.Fatal("disabled feature created collector ledger", err)
	}
	settings.RemoteOperations.Enabled = true
	writeSettings()
	response, err = read.Call(ctx, api.ListServiceOperations, api.Request{Params: params})
	if err != nil || response.Status != 200 || json.Unmarshal(response.Body, &policy) != nil || !policy.Enabled {
		t.Fatal(string(response.Body), err)
	}
	input := api.ServiceOperationInput{RequestID: strings.Repeat("a", 32), GrantID: "restart-demo", Version: policy.Version, Reason: "本人确认的原生夹具重启", Confirmed: true}
	body, _ := json.Marshal(input)
	for _, caller := range []*api.MCPHTTPClient{read, limited} {
		result, err := caller.Call(ctx, api.RestartService, api.Request{Params: params, Body: body})
		if err != nil || result.Status != 403 {
			t.Fatal(result, err)
		}
	}
	view, err := store.OpenView(ctx, filepath.Join(home, "data", "lgmon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	metricCount := func() int64 {
		t.Helper()
		result, err := view.Query(ctx, `SELECT count(*) FROM system_metrics`)
		if err != nil || len(result.Rows) != 1 {
			t.Fatal(result, err)
		}
		count, err := strconv.ParseInt(fmt.Sprint(result.Rows[0][0]), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return count
	}
	before := metricCount()
	execution := make(chan api.MCPResponse, 1)
	executionErr := make(chan error, 1)
	go func() {
		result, err := admin.Call(ctx, api.RestartService, api.Request{Params: params, Body: body})
		execution <- result
		executionErr <- err
	}()
	select {
	case <-executor.entered:
	case <-ctx.Done():
		t.Fatal("authenticated mTLS control did not reach fake executor")
	}
	for {
		if metricCount() > before {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("blocked service operation stopped telemetry persistence")
		case <-time.After(50 * time.Millisecond):
		}
	}
	close(executor.release)
	if err := <-executionErr; err != nil {
		t.Fatal(err)
	}
	if result := <-execution; result.Status != 200 {
		t.Fatal(string(result.Body), result.Status)
	}
	response, err = admin.Call(ctx, api.RestartService, api.Request{Params: params, Body: body})
	if err != nil || response.Status != 200 || executor.calls.Load() != 1 {
		t.Fatal("request repeated execution", string(response.Body), err, executor.calls.Load())
	}
	response, err = read.Call(ctx, api.GetServiceOperation, api.Request{Params: map[string]string{"id": input.RequestID}})
	var result store.OperationRequest
	if err != nil || response.Status != 200 || json.Unmarshal(response.Body, &result) != nil || result.State != "succeeded" || result.Service != "FakeService" || result.Actor != "fake-admin" {
		t.Fatal(string(response.Body), err)
	}
	settings.RemoteOperations.Enabled = false
	writeSettings()
	input.RequestID = strings.Repeat("b", 32)
	body, _ = json.Marshal(input)
	response, err = admin.Call(ctx, api.RestartService, api.Request{Params: params, Body: body})
	if err != nil || response.Status != 403 || executor.calls.Load() != 1 {
		t.Fatal("revoked local policy executed", string(response.Body), err)
	}
}

func TestNativeServiceOperationsBrowser(t *testing.T) {
	f := newAPIFixtureWithHTTPClock(t, time.Date(2026, 11, 9, 6, 0, 0, 0, time.UTC), time.Now)
	source, _ := attachMonitorOperations(t, f)
	f.samples(t)
	runNativeWebBrowser(t, f, "operations.spec.ts")
	if source.calls.Load() != 1 || len(f.client.sent) != 0 || f.hookFactories != 0 {
		t.Fatal("browser did not execute exactly the fake operation, or attempted notification", source.calls.Load())
	}
}
