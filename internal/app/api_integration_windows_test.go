//go:build integration && windows

package app

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNativeMonitorHTTPAuthRevocationAndShutdown(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "config")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	serviceFile := filepath.Join(t.TempDir(), "services.json")
	if err := os.WriteFile(serviceFile, []byte(`{"schemaVersion":1,"apps":[{"id":"fake-native-import","name":"虚构原生导入","ports":[43210],"env":{"password":"fake-private-source"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, err := json.Marshal(config.Settings{HTTPListen: "127.0.0.1:0", ServiceSwitch: config.ServiceSwitchSettings{Path: serviceFile}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "settings.yaml"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"targets.yaml":   "targets: [{id: fake-absent, match: {process: lgmon-fixture-not-present.exe}}]\n",
		"notifiers.yaml": "feishuApps: [{id: fake-app, appId: cli_fake_http, secretRef: fake-ref, domain: feishu}]\nnotifiers: [{id: fake-channel, type: feishu_app, app: fake-app, chatId: oc_fake_http}]\n",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	vault, err := secrets.New(home, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Set(context.Background(), "fake-ref", []byte("fake-native-http-secret")); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(home, "data", "auth.db")
	authDB, err := store.OpenAuthDB(context.Background(), authPath, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	hash := api.TokenHash("fake-native-read")
	row, err := authDB.AddAPIToken(context.Background(), hash[:], "fake-native", "read")
	if err != nil {
		authDB.Close()
		t.Fatal(err)
	}
	adminHash := api.TokenHash("fake-native-admin")
	if _, err := authDB.AddAPIToken(context.Background(), adminHash[:], "fake-native-admin", "admin"); err != nil {
		authDB.Close()
		t.Fatal(err)
	}
	if err := authDB.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 2)
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		runErr = RunLocal(ctx, LocalOptions{Home: home, Role: "both", Listen: "127.0.0.1:0", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), HTTPReady: func(address string) { ready <- address }, NewClient: func(config.FeishuApp, []byte) (feishu.Client, error) { return &apiClient{}, nil }})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if runErr != nil {
				t.Error(runErr)
			}
		case <-time.After(8 * time.Second):
			t.Error("native HTTP monitor did not stop")
		}
	})
	var address string
	select {
	case address = <-ready:
	case <-done:
		t.Fatalf("monitor startup failed: %v", runErr)
	case <-time.After(10 * time.Second):
		t.Fatal("HTTP listener did not start")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	request := func(token string, want int) {
		t.Helper()
		r, err := http.NewRequest("GET", listenerURL(address)+"/api/v1/collectors", nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != want || !json.Valid(data) {
			t.Fatalf("native HTTP status=%d body=%s err=%v", response.StatusCode, data, err)
		}
	}
	request("", 401)
	request("fake-native-read", 200)
	var mcpStatus atomic.Int32
	session := mcpSession(t, listenerURL(address), "fake-native-read", false, &mcpStatus)
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 34 {
		t.Fatalf("native MCP tool discovery failed: %v", err)
	}
	exported, exportErr := session.CallTool(ctx, &mcp.CallToolParams{Name: "export_metrics", Arguments: map[string]any{}})
	if exportErr != nil || exported.IsError || exported.StructuredContent == nil {
		t.Fatalf("native MCP persistent metrics failed: %v %+v", exportErr, exported)
	}
	id, err := (collector.FileIDStore{Home: home}).LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	processDeadline := time.NewTimer(30 * time.Second)
	defer processDeadline.Stop()
	for {
		callCtx, stopCall := context.WithTimeout(ctx, 12*time.Second)
		result, callErr := session.CallTool(callCtx, &mcp.CallToolParams{Name: "list_processes", Arguments: map[string]any{"id": id, "name": filepath.Base(os.Args[0])}})
		stopCall()
		if callErr != nil {
			t.Fatalf("native MCP process request failed: %v", callErr)
		}
		if !result.IsError {
			data, err := json.Marshal(result.StructuredContent)
			var snapshot collector.ProcessList
			if err != nil || json.Unmarshal(data, &snapshot) != nil || snapshot.CollectedAt <= 0 || strings.Contains(string(data), `"cmdline"`) {
				t.Fatal("native process snapshot has invalid shape or command line")
			}
			found := false
			for _, process := range snapshot.Processes {
				found = found || process.PID == int32(os.Getpid())
			}
			if !found {
				t.Fatal("native regular snapshot did not contain the test's own process")
			}
			break
		}
		select {
		case <-processDeadline.C:
			t.Fatal("native collector did not answer a process query")
		case <-time.After(2100 * time.Millisecond):
		}
	}
	importPreview := mcpCall(t, session, "preview_service_switch_import", map[string]any{"id": id, "publicProbes": false}, false)
	var importPlan config.ServiceSwitchPreview
	if err := json.Unmarshal([]byte(importPreview), &importPlan); err != nil || len(importPlan.Items) != 1 || importPlan.Items[0].TargetID != "fake-native-import" || strings.Contains(importPreview, "fake-private-source") {
		t.Fatalf("native import preview=%s %v", importPreview, err)
	}
	if !strings.Contains(mcpCall(t, session, "import_service_switch", map[string]any{"id": id, "sha256": importPlan.SHA256, "targetsVersion": importPlan.TargetsVersion}, true), "insufficient_scope") {
		t.Fatal("native read token imported targets")
	}
	streamCtx, stopStream := context.WithTimeout(ctx, 30*time.Second)
	streamRequest, err := http.NewRequestWithContext(streamCtx, "GET", listenerURL(address)+"/api/v1/stream", nil)
	if err != nil {
		stopStream()
		t.Fatal(err)
	}
	streamRequest.Header.Set("Authorization", "Bearer fake-native-read")
	streamClient := &http.Client{}
	defer streamClient.CloseIdleConnections()
	streamResponse, err := streamClient.Do(streamRequest)
	if err != nil {
		stopStream()
		t.Fatal(err)
	}
	defer streamResponse.Body.Close()
	ruleRequest, err := http.NewRequestWithContext(ctx, "PUT", listenerURL(address)+"/api/v1/config/rules", strings.NewReader("rules: [{id: fake-down, type: process_absent, target: fake-absent, for: 0s, severity: warning, notify: [fake-channel]}]\n"))
	if err != nil {
		stopStream()
		t.Fatal(err)
	}
	ruleRequest.Header.Set("Authorization", "Bearer fake-native-admin")
	ruleRequest.Header.Set("Content-Type", "application/yaml")
	ruleResponse, err := client.Do(ruleRequest)
	if err != nil {
		stopStream()
		t.Fatal(err)
	}
	ruleBody, err := io.ReadAll(ruleResponse.Body)
	ruleResponse.Body.Close()
	if err != nil || ruleResponse.StatusCode != 200 {
		stopStream()
		t.Fatalf("native configuration update failed: %d %s %v", ruleResponse.StatusCode, ruleBody, err)
	}
	seenSample, seenAlert, seenHeartbeat := false, false, false
	scanner := bufio.NewScanner(streamResponse.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: sample") {
			seenSample = true
		}
		if strings.HasPrefix(line, "event: alert") {
			seenAlert = true
		}
		if line == ": heartbeat" {
			seenHeartbeat = true
		}
		if seenSample && seenAlert && seenHeartbeat {
			break
		}
	}
	stopStream()
	streamResponse.Body.Close()
	if !seenSample || !seenAlert || !seenHeartbeat {
		t.Fatalf("native runtime did not deliver SSE data and keepalive: sample=%t alert=%t heartbeat=%t err=%v", seenSample, seenAlert, seenHeartbeat, scanner.Err())
	}
	for _, endpoint := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/api/v1/query", `{"sql":"SELECT id,hostname FROM collectors"}`, 200},
		{"GET", "/api/v1/schema", "", 200},
		{"POST", "/api/v1/query", `{"sql":"SELECT * FROM api_tokens"}`, 400},
	} {
		req, err := http.NewRequestWithContext(ctx, endpoint.method, listenerURL(address)+endpoint.path, strings.NewReader(endpoint.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer fake-native-read")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != endpoint.status || !json.Valid(data) {
			t.Fatalf("native SQL/schema endpoint: %s %d %s %v", endpoint.path, response.StatusCode, data, err)
		}
	}
	if err := secrets.NativePathPolicy().Check(authPath); err != nil {
		t.Fatalf("auth.db is not protected: %v", err)
	}
	// CLI token management must be able to secure data while the monitor owns its databases.
	if err := secrets.NativePathPolicy().ProtectTree(filepath.Join(home, "data"), false); err != nil {
		t.Fatal(err)
	}
	authDB, err = store.OpenAuthDB(context.Background(), authPath, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer authDB.Close()
	if err := authDB.RevokeAPIToken(context.Background(), row.ID); err != nil {
		t.Fatal(err)
	}
	request("fake-native-read", 401)
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_collectors", Arguments: map[string]any{}}); err == nil || mcpStatus.Load() != 401 {
		t.Fatalf("revoked token retained MCP access: %v", err)
	}
	cancel()
	select {
	case <-done:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("monitor shutdown timed out")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("native monitor retained its HTTP port: %v", err)
	}
	listener.Close()
}
