//go:build integration && windows

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
)

func TestCollectorProcessStopsOfflineWithin35SecondsAndRestartRecovers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer cancel()
	root := t.TempDir()
	monitorHome, collectorHome := filepath.Join(root, "monitor"), filepath.Join(root, "collector")
	directory := filepath.Join(monitorHome, "config")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	notifiers := "feishuApps:\n- {id: fake-app, appId: cli_fake_offline, secretRef: fake-ref, domain: feishu}\nnotifiers:\n- {id: fake-channel, type: feishu_app, app: fake-app, chatId: oc_fake_offline}\n"
	if err := os.WriteFile(filepath.Join(directory, "notifiers.yaml"), []byte(notifiers), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	monitorKeys, err := secrets.New(monitorHome, logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := monitorKeys.Set(ctx, "fake-ref", []byte("fake-offline-secret")); err != nil {
		t.Fatal(err)
	}
	client := &runtimeClient{wire: make(chan feishu.CardMessage, 8), release: make(chan struct{})}
	close(client.release)
	monitorDone := make(chan error, 1)
	go func() {
		monitorDone <- RunLocal(ctx, LocalOptions{Home: monitorHome, Role: "monitor", Listen: "127.0.0.1:0", Version: "fake-offline", Logger: logger, Host: roleHost{role: "monitor"}, NewClient: func(config.FeishuApp, []byte) (feishu.Client, error) { return remoteCardClient{client}, nil }})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-monitorDone:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("own monitor did not stop")
		}
	})
	until := func(limit time.Duration, condition func() bool) bool {
		deadline := time.NewTimer(limit)
		defer deadline.Stop()
		for {
			if condition() {
				return true
			}
			select {
			case <-ctx.Done():
				return false
			case <-deadline.C:
				return false
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	address := ""
	if !until(5*time.Second, func() bool {
		data, err := os.ReadFile(filepath.Join(monitorHome, "data", "pki", "endpoint.json"))
		if err != nil {
			return false
		}
		var endpoint struct {
			Address string `json:"address"`
		}
		if json.Unmarshal(data, &endpoint) != nil {
			return false
		}
		address = endpoint.Address
		return address != ""
	}) {
		t.Fatal("own monitor endpoint did not become ready")
	}
	admin, err := transport.ConnectAdmin(ctx, transport.FileState{Home: monitorHome}, monitorKeys, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	code, err := lgmonv1.NewPairingClient(admin).NewCode(ctx, &lgmonv1.NewCodeRequest{TtlMillis: 60000})
	if err != nil {
		t.Fatal(err)
	}
	id, err := (collector.FileIDStore{Home: collectorHome}).LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	collectorKeys, err := secrets.New(collectorHome, logger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.Join(ctx, transport.JoinOptions{Address: address, CollectorID: id, Hostname: "RemoteHost", Code: code.Code, Fingerprint: code.Fingerprint, Keys: collectorKeys, State: transport.FileState{Home: collectorHome}}); err != nil {
		t.Fatal(err)
	}
	type child struct {
		cmd    *exec.Cmd
		done   chan struct{}
		stop   string
		output bytes.Buffer
		err    error
		killed bool
	}
	start := func() *child {
		t.Helper()
		process := &child{done: make(chan struct{}), stop: filepath.Join(root, fmt.Sprintf("stop-%d", time.Now().UnixNano()))}
		process.cmd = exec.Command(os.Args[0], "-test.run=^TestRemoteRoleProcess$", "--", "--role", "collector", "--home", collectorHome, "--stop", process.stop)
		process.cmd.Env = append(os.Environ(), "LGMON_ROLE_HELPER=1")
		process.cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		process.cmd.Stdout, process.cmd.Stderr = &process.output, &process.output
		if err := process.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		go func() { process.err = process.cmd.Wait(); close(process.done) }()
		t.Cleanup(func() {
			if !process.killed {
				if err := os.WriteFile(process.stop, []byte("stop"), 0o600); err != nil {
					t.Error(err)
				}
			}
			select {
			case <-process.done:
			case <-time.After(5 * time.Second):
				process.cmd.Process.Kill()
				<-process.done
				t.Error("own collector did not stop gracefully")
			}
			if !process.killed && process.err != nil {
				t.Errorf("own collector: %v %s", process.err, process.output.String())
			}
		})
		return process
	}
	process := start()
	cachePath := filepath.Join(collectorHome, "data", "collector-config", "targets.json")
	if !until(5*time.Second, func() bool { _, err := os.Stat(cachePath); return err == nil }) {
		t.Fatal("empty-target collector did not connect")
	}
	reader := openOfflineReader(t, filepath.Join(monitorHome, "data", "lgmon.db"))
	metadata := func() (lastSeen int64) {
		_ = reader.QueryRowContext(ctx, "SELECT last_seen FROM collectors WHERE id=?", id).Scan(&lastSeen)
		return lastSeen
	}
	initial := metadata()
	if initial <= 0 {
		t.Fatal("connected collector metadata missing")
	}
	if !until(12*time.Second, func() bool { return metadata() >= initial+9000 }) {
		t.Fatal("empty-target collector did not update last_seen after 10s heartbeat")
	}
	select {
	case message := <-client.wire:
		t.Fatalf("healthy collector alerted: %+v", message)
	default:
	}
	killedAt := time.Now()
	process.killed = true
	if err := process.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-process.done
	var fired feishu.CardMessage
	select {
	case fired = <-client.wire:
	case <-time.After(35 * time.Second):
		t.Fatal("no offline alert within 35 seconds after stopping own collector")
	}
	if fired.State != "firing" {
		t.Fatalf("wrong offline transition: %+v", fired)
	}
	t.Logf("Own collector process stopped; default offline alert in %s (fake Feishu)", time.Since(killedAt))
	start()
	restartedAt := time.Now()
	select {
	case recovered := <-client.wire:
		if recovered.State != "resolved" || recovered.AlertID != fired.AlertID {
			t.Fatalf("restart did not recover same incident: %+v", recovered)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("restarted collector did not recover")
	}
	t.Logf("Own collector restart recovered the same alert in %s (fake Feishu)", time.Since(restartedAt))
	view, err := store.OpenView(ctx, filepath.Join(monitorHome, "data", "lgmon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	if !until(time.Second, func() bool {
		receipt, err := view.Delivery(ctx, fired.AlertID, "resolved", "fake-channel")
		return err == nil && receipt.OK
	}) {
		t.Fatal("recovery notification was not audited")
	}
}
