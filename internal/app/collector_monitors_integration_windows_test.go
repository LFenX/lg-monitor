//go:build integration && windows

package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"github.com/shirou/gopsutil/v4/process"
)

// Build the production collector without the test binary's unrelated fixtures and coverage machinery.
const multiMonitorHelper = `package main
import("context";"fmt";"os";"io";"time";"path/filepath";"log/slog";"runtime";"runtime/pprof";"github.com/LFenX/lg-monitor/internal/app";"github.com/LFenX/lg-monitor/internal/config")
func profile()func(){root:=os.Getenv("LGMON_MULTI_PROFILE");if root==""{return func(){}};root+=fmt.Sprintf(".%d",os.Getpid());f,e:=os.Create(root+".cpu");if e!=nil{panic(e)};pprof.StartCPUProfile(f);return func(){pprof.StopCPUProfile();f.Close();h,e:=os.Create(root+".heap");if e!=nil{panic(e)};pprof.WriteHeapProfile(h);h.Close()}}
type policy struct{home string}
func(p policy)ReadOperationPolicy(context.Context)(config.RemoteOperations,error){s,e:=config.LoadSettings(os.DirFS(filepath.Join(p.home,"config")));return s.RemoteOperations,e}
type executor struct{home string}
func(e executor)Restart(_ context.Context,name string)error{if name!="FakeOwnedService"{return fmt.Errorf("non-fixture service")};f,err:=os.OpenFile(filepath.Join(e.home,"data","fake-restarts"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{return err};defer f.Close();_,err=f.WriteString("fixture\n");return err}
func main(){
 if os.Args[1]=="--demo"{io.Copy(io.Discard,os.Stdin);return}
 defer profile()()
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 go func(){ticker:=time.NewTicker(20*time.Millisecond);defer ticker.Stop();for{select{case<-ctx.Done():return;case<-ticker.C:if _,e:=os.Stat(os.Args[2]);e==nil{var m runtime.MemStats;runtime.ReadMemStats(&m);fmt.Fprintf(os.Stderr,"MEMORY sys=%d heapInuse=%d heapSys=%d heapReleased=%d stack=%d\n",m.Sys,m.HeapInuse,m.HeapSys,m.HeapReleased,m.StackInuse);cancel();return}}}}()
 if e:=app.RunLocal(ctx,app.LocalOptions{Home:os.Args[1],Role:"collector",NoTray:true,OperationPolicy:policy{os.Args[1]},ServiceRestarter:executor{os.Args[1]},Logger:slog.Default()});e!=nil{fmt.Fprintln(os.Stderr,e);os.Exit(1)}
}`

func TestNativeMultiMonitorIsolationReplayRevocationAndResources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	root, logger := t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))
	collectorHome := filepath.Join(root, "collector")
	if err := os.MkdirAll(filepath.Join(collectorHome, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(collectorHome, "scripts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collectorHome, "scripts", "own-multi.cmd"), []byte("@echo off\r\necho {\"ok\":true,\"message\":\"fixture\",\"metrics\":{\"owned\":7}}\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	settings := config.Settings{CollectorMonitors: []config.CollectorMonitor{{ID: "company"}, {ID: "broken"}, {ID: "disabled", Enabled: new(bool)}}, RemoteOperations: config.RemoteOperations{Enabled: true, Services: []config.ServiceGrant{{ID: "owned", Service: "FakeOwnedService"}}}}
	writeSettings := func() {
		t.Helper()
		raw, _ := json.Marshal(settings)
		if err := os.WriteFile(filepath.Join(collectorHome, "config", "settings.yaml"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeSettings()
	if err := secrets.NativePathPolicy().ProtectTree(collectorHome, false); err != nil {
		t.Fatal(err)
	}
	id, err := (collector.FileIDStore{Home: collectorHome}).LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	vault, err := secrets.New(collectorHome, logger)
	if err != nil {
		t.Fatal(err)
	}
	servers := []*httptest.Server{
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })),
		httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) })),
	}
	for _, server := range servers {
		t.Cleanup(server.Close)
	}
	type monitorNode struct {
		home, address, httpAddress string
		stop                       context.CancelFunc
		done                       chan error
		running                    bool
	}
	nodes := []*monitorNode{{home: filepath.Join(root, "first")}, {home: filepath.Join(root, "second")}}
	startMonitor := func(node *monitorNode) {
		t.Helper()
		local, stop := context.WithCancel(ctx)
		node.stop = stop
		node.done = make(chan error, 1)
		node.running = true
		ready := make(chan string, 1)
		listen := node.address
		if listen == "" {
			listen = "127.0.0.1:0"
		}
		go func() {
			node.done <- RunLocal(local, LocalOptions{Home: node.home, Role: "monitor", Listen: listen, NoTray: true, Logger: logger, HTTPReady: func(address string) { ready <- address }})
		}()
		select {
		case node.httpAddress = <-ready:
		case err := <-node.done:
			node.running = false
			t.Fatalf("monitor startup: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		data, err := (transport.FileState{Home: node.home}).Load(ctx, "endpoint")
		if err != nil {
			t.Fatal(err)
		}
		var endpoint struct{ Address string }
		if json.Unmarshal(data, &endpoint) != nil {
			t.Fatal("endpoint JSON")
		}
		node.address = endpoint.Address
	}
	stopMonitor := func(node *monitorNode) {
		t.Helper()
		if !node.running {
			return
		}
		node.stop()
		select {
		case err := <-node.done:
			node.running = false
			if err != nil {
				t.Error(err)
			}
		case <-time.After(8 * time.Second):
			t.Fatal("owned monitor did not stop")
		}
	}
	for index, node := range nodes {
		if err := os.MkdirAll(filepath.Join(node.home, "config"), 0700); err != nil {
			t.Fatal(err)
		}
		_, port, err := net.SplitHostPort(strings.TrimPrefix(servers[index].URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		targets := fmt.Sprintf("targets:\n- id: shared\n  match: {port: %s}\n  probes:\n  - {type: http, url: '%s', interval: 1s, timeout: 500ms}\n  - {type: script, script: own-multi.cmd, interval: 2s, timeout: 1s}\n- id: exit-demo\n  match: {process: collector-fixture.exe, cmdline: 'own-m303-demo-%s'}\n", port, servers[index].URL, id)
		for name, data := range map[string]string{"targets.yaml": targets, "settings.yaml": "httpListen: 127.0.0.1:0\n"} {
			if err := os.WriteFile(filepath.Join(node.home, "config", name), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
		auth, err := store.OpenAuthDB(ctx, filepath.Join(node.home, "data", "auth.db"), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		hash := api.TokenHash("fake-multi-admin")
		if _, err := auth.AddAPIToken(ctx, hash[:], "fixture", "admin"); err != nil {
			t.Fatal(err)
		}
		if err := auth.Close(); err != nil {
			t.Fatal(err)
		}
		startMonitor(node)
		t.Cleanup(func() { stopMonitor(node) })
		monitorVault, err := secrets.New(node.home, logger)
		if err != nil {
			t.Fatal(err)
		}
		connection, err := transport.ConnectAdmin(ctx, transport.FileState{Home: node.home}, monitorVault, time.Now, nil)
		if err != nil {
			t.Fatal(err)
		}
		code, err := lgmonv1.NewPairingClient(connection).NewCode(ctx, &lgmonv1.NewCodeRequest{TtlMillis: 60000})
		connection.Close()
		if err != nil {
			t.Fatal(err)
		}
		name := ""
		if index == 1 {
			name = "company"
		}
		if _, err := transport.Join(ctx, transport.JoinOptions{Address: node.address, CollectorID: id, Hostname: "FakeMultiHost", Code: code.Code, Fingerprint: code.Fingerprint, Keys: vault, State: transport.FileState{Home: collectorHome, Monitor: name}}); err != nil {
			t.Fatal(err)
		}
	}
	// This malformed enabled slot must not prevent either healthy paired slot from starting.
	if err := (transport.FileState{Home: collectorHome, Monitor: "broken"}).Save(ctx, "collector", []byte(`{"version":99}`), false); err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	devHome := filepath.Join(repo, "dev-home")
	if err := os.MkdirAll(devHome, 0700); err != nil {
		t.Fatal(err)
	}
	source, err := os.CreateTemp(devHome, "m303-helper-*.go")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(source.Name()) })
	if _, err := source.WriteString(multiMonitorHelper); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "collector-fixture.exe")
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags=-s -w", "-o", executable, source.Name())
	build.Dir = repo
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	build.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v %s", err, output)
	}
	type childProcess struct {
		command *exec.Cmd
		done    chan error
		output  bytes.Buffer
		stop    string
		running bool
	}
	var children []*childProcess
	startCollector := func() *childProcess {
		t.Helper()
		child := &childProcess{done: make(chan error, 1), stop: filepath.Join(root, fmt.Sprintf("stop-%d", len(children))), running: true}
		child.command = exec.CommandContext(ctx, executable, collectorHome, child.stop)
		child.command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		child.command.Stdout = &child.output
		child.command.Stderr = &child.output
		if err := child.command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Logf("owned multi-monitor fixture PID=%d", child.command.Process.Pid)
		go func() { child.done <- child.command.Wait() }()
		children = append(children, child)
		return child
	}
	stopCollector := func(child *childProcess) {
		t.Helper()
		if !child.running {
			return
		}
		if err := os.WriteFile(child.stop, []byte("stop"), 0600); err != nil {
			t.Fatal(err)
		}
		at := time.Now()
		select {
		case err := <-child.done:
			child.running = false
			if err != nil {
				t.Fatalf("collector: %v %s", err, child.output.String())
			}
			t.Logf("multi-monitor owned collector graceful exit: %s", time.Since(at))
		case <-time.After(5 * time.Second):
			child.command.Process.Kill()
			<-child.done
			child.running = false
			query := exec.Command("logman", "query", "-ets")
			query.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			output, err := query.CombinedOutput()
			if err == nil {
				for _, line := range strings.Split(string(output), "\n") {
					fields := strings.Fields(line)
					if len(fields) > 0 && strings.HasPrefix(fields[0], fmt.Sprintf("lgmon-process-%d-", child.command.Process.Pid)) {
						stop := exec.Command("logman", "stop", fields[0], "-ets")
						stop.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
						if result, err := stop.CombinedOutput(); err != nil {
							t.Errorf("owned trace cleanup: %v %s", err, result)
						}
					}
				}
			}
			t.Fatal("collector pipelines did not stop within 5s")
		}
	}
	t.Cleanup(func() {
		for _, child := range children {
			stopCollector(child)
		}
	})
	child := startCollector()
	waitFor := func(label string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if condition() {
				return
			}
			select {
			case err := <-child.done:
				child.running = false
				t.Fatalf("collector exited during %s: %v %s", label, err, child.output.String())
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(30 * time.Millisecond):
			}
		}
		t.Fatalf("%s timed out", label)
	}
	latest := func(node *monitorNode) (string, int, int64) {
		view, err := store.OpenView(ctx, filepath.Join(node.home, "data", "lgmon.db"))
		if err != nil {
			return "", 0, 0
		}
		defer view.Close()
		rows, err := view.CurrentTargets(ctx)
		if err != nil {
			return "", 0, 0
		}
		var state string
		for _, row := range rows {
			if row.CollectorID == id && row.TargetID == "shared" {
				state = row.State
			}
		}
		if state == "" {
			return "", 0, 0
		}
		reader := openOfflineReader(t, filepath.Join(node.home, "data", "lgmon.db"))
		defer reader.Close()
		var status int
		var count int64
		if reader.QueryRow(`SELECT ok FROM probe_results WHERE collector_id=? AND target_id='shared' AND probe=0 ORDER BY ts DESC LIMIT 1`, id).Scan(&status) != nil {
			return "", 0, 0
		}
		if reader.QueryRow(`SELECT count(*) FROM probe_results WHERE collector_id=?`, id).Scan(&count) != nil {
			return "", 0, 0
		}
		return state, status, count
	}
	waitFor("independent same-ID targets and probes", func() bool {
		a, as, _ := latest(nodes[0])
		b, bs, _ := latest(nodes[1])
		return a == "up" && as == 1 && b == "up" && bs == 0
	})
	for _, node := range nodes {
		waitFor("root script used by each monitor", func() bool {
			reader := openOfflineReader(t, filepath.Join(node.home, "data", "lgmon.db"))
			defer reader.Close()
			var value float64
			err := reader.QueryRow(`SELECT value FROM script_metrics WHERE collector_id=? AND target_id='shared' AND name='owned' LIMIT 1`, id).Scan(&value)
			return err == nil && value == 7
		})
	}
	policy := func(node *monitorNode) collector.OperationPolicy {
		t.Helper()
		client, err := api.NewMCPHTTPClient(api.MCPHTTPClientOptions{BaseURL: "http://" + node.httpAddress, Token: "fake-multi-admin"})
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Call(ctx, api.ListServiceOperations, api.Request{Params: map[string]string{"id": id}})
		var p collector.OperationPolicy
		if err != nil || response.Status != 200 {
			return p
		}
		if json.Unmarshal(response.Body, &p) != nil {
			t.Fatalf("operation policy: %d %s %v", response.Status, response.Body, err)
		}
		return p
	}
	if !policy(nodes[0]).Enabled || policy(nodes[1]).Enabled {
		t.Fatal("secondary inherited primary operations grant")
	}
	first := openOfflineReader(t, filepath.Join(collectorHome, "data", "outbox.db"))
	defer first.Close()
	second := openOfflineReader(t, filepath.Join(collectorHome, "data", "monitors", "company", "outbox.db"))
	defer second.Close()
	state := func(reader interface{ QueryRow(string, ...any) *sql.Row }) (int64, int64) {
		var allocated, acked int64
		if err := reader.QueryRow(`SELECT allocated,acked FROM outbox_meta WHERE singleton=1`).Scan(&allocated, &acked); err != nil {
			t.Fatal(err)
		}
		return allocated, acked
	}
	stopMonitor(nodes[0])
	_, _, beforeHealthy := latest(nodes[1])
	_, offlineAck := state(first)
	waitFor("healthy monitor continues while primary offline", func() bool {
		_, _, count := latest(nodes[1])
		allocated, acked := state(first)
		return count >= beforeHealthy+4 && allocated > acked && acked >= offlineAck
	})
	_, offlineAck = state(first)
	time.Sleep(1200 * time.Millisecond)
	if _, ack := state(first); ack != offlineAck {
		t.Fatal("healthy acknowledgement cleaned offline queue")
	}
	stopCollector(child)
	startMonitor(nodes[0])
	child = startCollector()
	waitFor("independent persisted replay drains", func() bool {
		a, aa := state(first)
		b, ba := state(second)
		_, _, count := latest(nodes[0])
		return a == aa && b == ba && count > 4
	})
	// Both explicit local opt-in and the existing local policy are required.
	stopCollector(child)
	settings.CollectorMonitors[0].AllowOperations = true
	writeSettings()
	child = startCollector()
	waitFor("locally enabled secondary operations", func() bool { return policy(nodes[1]).Enabled })
	requestID := strings.Repeat("a", 32)
	for _, node := range nodes {
		p := policy(node)
		client, err := api.NewMCPHTTPClient(api.MCPHTTPClientOptions{BaseURL: "http://" + node.httpAddress, Token: "fake-multi-admin"})
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(api.ServiceOperationInput{RequestID: requestID, GrantID: "owned", Version: p.Version, Reason: "owned multi-monitor fixture", Confirmed: true})
		response, err := client.Call(ctx, api.RestartService, api.Request{Params: map[string]string{"id": id}, Body: body})
		if err != nil || response.Status >= 300 {
			t.Fatalf("fake restart: %d %s %v", response.Status, response.Body, err)
		}
	}
	waitFor("operation request IDs isolated by monitor", func() bool {
		raw, _ := os.ReadFile(filepath.Join(collectorHome, "data", "fake-restarts"))
		return strings.Count(string(raw), "fixture") == 2
	})
	monitorVault, err := secrets.New(nodes[0].home, logger)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := transport.ConnectAdmin(ctx, transport.FileState{Home: nodes[0].home}, monitorVault, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = lgmonv1.NewPairingClient(connection).RevokeCollector(ctx, &lgmonv1.RevokeCollectorRequest{Id: id})
	connection.Close()
	if err != nil {
		t.Fatal(err)
	}
	_, _, beforeHealthy = latest(nodes[1])
	waitFor("secondary survives primary revocation", func() bool { _, _, count := latest(nodes[1]); return count >= beforeHealthy+4 })
	demo := exec.CommandContext(ctx, executable, "--demo", "own-m303-demo-"+id)
	demo.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	input, err := demo.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := demo.Start(); err != nil {
		t.Fatal(err)
	}
	demoDone := make(chan error, 1)
	go func() { demoDone <- demo.Wait() }()
	demoRunning := true
	t.Cleanup(func() {
		if demoRunning {
			input.Close()
			select {
			case <-demoDone:
			case <-time.After(5 * time.Second):
				demo.Process.Kill()
				<-demoDone
			}
		}
	})
	demoState := func() string {
		view, err := store.OpenView(ctx, filepath.Join(nodes[1].home, "data", "lgmon.db"))
		if err != nil {
			return ""
		}
		defer view.Close()
		rows, err := view.CurrentTargets(ctx)
		if err != nil {
			return ""
		}
		for _, row := range rows {
			if row.CollectorID == id && row.TargetID == "exit-demo" {
				return row.State
			}
		}
		return ""
	}
	waitFor("owned demo up through healthy monitor", func() bool { return demoState() == "up" })
	exitAt := time.Now()
	input.Close()
	if err := <-demoDone; err != nil {
		t.Fatal(err)
	}
	demoRunning = false
	for demoState() != "down" && time.Since(exitAt) < time.Second {
		time.Sleep(10 * time.Millisecond)
	}
	if elapsed := time.Since(exitAt); demoState() != "down" || elapsed >= time.Second {
		t.Fatalf("shared snapshots/resource budget hid process exit: %s", elapsed)
	} else {
		t.Logf("multi-monitor production ETW exit persisted: %s", elapsed)
	}
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(30 * time.Second):
	}
	p, err := process.NewProcess(int32(child.command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := p.Times()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	var peak, peakCommitted uint64
	for index := 0; index < 60; index++ {
		memory, err := p.MemoryInfo()
		if err != nil {
			t.Fatal(err)
		}
		peak = max(peak, memory.RSS)
		peakCommitted = max(peakCommitted, memory.VMS)
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	final, err := p.Times()
	if err != nil {
		t.Fatal(err)
	}
	cpu := (final.Total() - initial.Total()) / time.Since(at).Seconds() / float64(runtime.NumCPU()) * 100
	t.Logf("native production collector, two independent monitors (one revoked): averageCPU=%.4f%% peakRSS=%d peakPrivateCommit=%d bytes duration=%s; same collector ID, mTLS, native sampling/probes/ETW active", cpu, peak, peakCommitted, time.Since(at))
	stopCollector(child)
	for _, line := range strings.Split(child.output.String(), "\n") {
		if strings.Contains(line, "MEMORY ") {
			t.Log(line)
		}
	}
	if cpu >= 1 {
		t.Fatalf("collector CPU exceeds PRD: %.4f%%", cpu)
	}
	if peak >= 40*1024*1024 {
		t.Fatalf("collector RSS exceeds PRD: %d", peak)
	}
	settings.RemoteOperations.Enabled = false
	settings.Collection = config.CollectionSettings{TargetInterval: "500ms", ProcessInterval: "1s", SystemInterval: "1s", ServiceInterval: "1s"}
	// Isolate timer cadence from ETW's additional, deliberately immediate snapshots.
	settings.ProcessEvents.Mode = "disabled"
	writeSettings()
	primaryState := filepath.Join(collectorHome, "data", "pki", "collector.json")
	if err := os.Rename(primaryState, primaryState+".fixture-backup"); err != nil {
		t.Fatal(err)
	}
	_, _, beforeHealthy = latest(nodes[1])
	configuredStart := time.Now().UnixMilli()
	child = startCollector()
	waitFor("secondary-only startup without default identity", func() bool { _, _, count := latest(nodes[1]); return count >= beforeHealthy+2 })
	if policy(nodes[1]).Enabled {
		t.Fatal("named local grant bypassed disabled global policy")
	}
	var sampleCount, metricCount int64
	var sampleFirst, sampleLast, metricFirst, metricLast int64
	waitFor("production nondefault target/system periods persist through mTLS", func() bool {
		reader := openOfflineReader(t, filepath.Join(nodes[1].home, "data", "lgmon.db"))
		defer reader.Close()
		if err := reader.QueryRow(`SELECT count(*),COALESCE(min(ts),0),COALESCE(max(ts),0) FROM process_samples WHERE collector_id=? AND target_id='shared' AND ts>=? AND interval_ms=500`, id, configuredStart).Scan(&sampleCount, &sampleFirst, &sampleLast); err != nil {
			t.Fatal(err)
		}
		if err := reader.QueryRow(`SELECT count(*),COALESCE(min(ts),0),COALESCE(max(ts),0) FROM system_metrics WHERE collector_id=? AND ts>=?`, id, configuredStart).Scan(&metricCount, &metricFirst, &metricLast); err != nil {
			t.Fatal(err)
		}
		return sampleCount >= 8 && metricCount >= 4
	})
	samplePeriod := float64(sampleLast-sampleFirst) / float64(sampleCount-1)
	metricPeriod := float64(metricLast-metricFirst) / float64(metricCount-1)
	if samplePeriod < 300 || samplePeriod > 900 || metricPeriod < 700 || metricPeriod > 1600 {
		t.Fatalf("configured periods ignored or facts duplicated: target=%.1fms system=%.1fms", samplePeriod, metricPeriod)
	}
	t.Logf("production configured periods through mTLS/SQLite: target=%.1fms (%d rows, interval_ms=500), system=%.1fms (%d rows)", samplePeriod, sampleCount, metricPeriod, metricCount)
	stopCollector(child)
	bothHome := filepath.Join(root, "both")
	if err := os.MkdirAll(filepath.Join(bothHome, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bothHome, "config", "settings.yaml"), []byte("httpListen: 127.0.0.1:0\ncollectorMonitors: [{id: company}]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bothID, err := (collector.FileIDStore{Home: bothHome}).LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	bothVault, err := secrets.New(bothHome, logger)
	if err != nil {
		t.Fatal(err)
	}
	monitorVault, err = secrets.New(nodes[1].home, logger)
	if err != nil {
		t.Fatal(err)
	}
	connection, err = transport.ConnectAdmin(ctx, transport.FileState{Home: nodes[1].home}, monitorVault, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	code, err := lgmonv1.NewPairingClient(connection).NewCode(ctx, &lgmonv1.NewCodeRequest{TtlMillis: 60000})
	connection.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Join(ctx, transport.JoinOptions{Address: nodes[1].address, CollectorID: bothID, Hostname: "FakeBothHost", Code: code.Code, Fingerprint: code.Fingerprint, Keys: bothVault, State: transport.FileState{Home: bothHome, Monitor: "company"}}); err != nil {
		t.Fatal(err)
	}
	bothCtx, bothCancel := context.WithCancel(ctx)
	bothDone := make(chan error, 1)
	go func() {
		bothDone <- RunLocal(bothCtx, LocalOptions{Home: bothHome, Role: "both", Listen: "127.0.0.1:0", NoTray: true, Logger: logger})
	}()
	t.Cleanup(func() {
		bothCancel()
		select {
		case err := <-bothDone:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(8 * time.Second):
			t.Error("owned both node did not stop")
		}
	})
	for _, home := range []string{bothHome, nodes[1].home} {
		waitFor("both role sends same ID to local and remote monitors", func() bool {
			reader := openOfflineReader(t, filepath.Join(home, "data", "lgmon.db"))
			defer reader.Close()
			var count int
			err := reader.QueryRow(`SELECT count(*) FROM system_metrics WHERE collector_id=?`, bothID).Scan(&count)
			return err == nil && count > 0
		})
	}
}
