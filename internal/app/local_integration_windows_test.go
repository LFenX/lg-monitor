//go:build integration && windows

package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestLocalRuntimeOwnListenerAndStoppedQueries(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	data := fmt.Sprintf("targets:\n- id: own-listener\n  match: {port: %d}\n", port)
	if err := os.WriteFile(filepath.Join(home, "config", "targets.yaml"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	finished := make(chan struct{})
	var runErr error
	var notice bytes.Buffer
	go func() {
		defer close(finished)
		runErr = RunLocal(ctx, LocalOptions{Home: home, Listen: "127.0.0.1:0", Version: "integration", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Notice: func(message string) error { _, err := fmt.Fprintln(&notice, message); return err }})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("local runtime did not release resources")
		}
	})
	path := filepath.Join(home, "data", "lgmon.db")
	waitState := func(want string) {
		t.Helper()
		started := time.Now()
		for {
			select {
			case <-finished:
				t.Fatalf("runtime exited before %s: %v", want, runErr)
			case <-ctx.Done():
				t.Fatalf("timed out waiting for %s", want)
			default:
			}
			if view, err := store.OpenView(ctx, path); err == nil {
				targets, queryErr := view.CurrentTargets(ctx)
				view.Close()
				if queryErr == nil && len(targets) == 1 && targets[0].State == want {
					t.Logf("Own listener reached %s in %s", want, time.Since(started))
					return
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitState("up")
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	waitState("down")
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("local runtime did not stop")
	}
	if runErr != nil || !strings.Contains(notice.String(), "feishu setup") {
		t.Fatalf("local runtime shutdown/notice: %v %s", runErr, notice.String())
	}
	view, err := store.OpenView(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	targets, err := view.CurrentTargets(context.Background())
	if err != nil || len(targets) != 1 || targets[0].State != "down" {
		t.Fatalf("stopped query: %+v %v", targets, err)
	}
}

func TestNativeM0FiveSecondRuleAndRecoveryWithFakeDelivery(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "owned demo") })}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	files := fstest.MapFS{
		"targets.yaml":   {Data: []byte(fmt.Sprintf("targets:\n- id: own-http\n  match: {port: %d}\n  probes:\n  - {type: http, url: 'http://%s/', interval: 2s, timeout: 1s}\n", port, address))},
		"rules.yaml":     {Data: []byte("rules:\n- id: own-http-down\n  type: port_closed\n  target: own-http\n  for: 5s\n  severity: critical\n  notify: [fake-channel]\n  template: detailed\n")},
		"notifiers.yaml": {Data: []byte("feishuApps:\n- {id: fake-app, appId: cli_fake_m0, secretRef: fake-ref, domain: feishu}\nnotifiers:\n- {id: fake-channel, type: feishu_app, app: fake-app, chatId: oc_fake_m0}\n")},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager, err := config.NewManager(config.FSLoader{FS: files}, logger)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "lgmon.db")
	db, err := store.Open(context.Background(), store.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	client := &runtimeClient{wire: make(chan feishu.CardMessage, 8), release: make(chan struct{})}
	close(client.release)
	prober := collector.NewProber(time.Now)
	t.Cleanup(prober.Close)
	runtime, err := NewRuntime(RuntimeOptions{ID: "fake-m0-collector", Hostname: "fake-host", Config: manager, Store: db, Secrets: runtimeVault{},
		Processes: collector.NewProcessSource(time.Now, logger), Ports: collector.NewPortSource(time.Now, logger), Probes: prober,
		NewClient: func(config.FeishuApp, []byte) (feishu.Client, error) { return client, nil }, Now: time.Now, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	finished := make(chan error, 1)
	go func() { finished <- runtime.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-finished:
			if err != nil {
				t.Errorf("runtime cleanup: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("runtime did not stop")
		}
	})
	view, err := store.OpenView(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { view.Close() })
	waitFor := func(label string, condition func() bool) {
		t.Helper()
		started := time.Now()
		for time.Since(started) < 15*time.Second {
			if condition() {
				t.Logf("%s in %s (fake Feishu, real Windows collection/SQLite)", label, time.Since(started))
				return
			}
			select {
			case err := <-finished:
				finished <- err
				t.Fatalf("runtime stopped: %v", err)
			default:
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s exceeded 15 seconds", label)
	}
	waitFor("initial up", func() bool {
		targets, err := view.CurrentTargets(ctx)
		return err == nil && len(targets) == 1 && targets[0].State == "up"
	})
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	alertID := ""
	waitState := func(state string) bool {
		alerts, err := view.Alerts(ctx, true)
		if err != nil || len(alerts) != 1 || alerts[0].State != state || (alertID != "" && alerts[0].ID != alertID) {
			return false
		}
		alertID = alerts[0].ID
		receipt, err := view.Delivery(ctx, alertID, state, "fake-channel")
		return err == nil && receipt.OK
	}
	waitFor("firing and successful audit", func() bool { return waitState("firing") })
	listener, err = net.Listen("tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	recovered := &http.Server{ReadHeaderTimeout: time.Second, Handler: server.Handler}
	go recovered.Serve(listener)
	t.Cleanup(func() { recovered.Close() })
	waitFor("resolved and successful audit", func() bool { return waitState("resolved") })
}
