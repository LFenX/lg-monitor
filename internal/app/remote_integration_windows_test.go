//go:build integration && windows

package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/store"
)

type roleHost struct{ role string }

func (h roleHost) Hostname(context.Context) (string, error) {
	if h.role == "collector" {
		return "RemoteHost", nil
	}
	return "MonitorHost", nil
}

type remoteCardClient struct{ *runtimeClient }

func (c remoteCardClient) SendCard(ctx context.Context, message feishu.CardMessage) (feishu.SendResult, error) {
	if !strings.Contains(string(message.Card), "RemoteHost") || strings.Contains(string(message.Card), "MonitorHost") {
		return feishu.SendResult{}, fmt.Errorf("remote alert used the wrong hostname")
	}
	return c.runtimeClient.SendCard(ctx, message)
}

func TestRemoteRoleProcess(t *testing.T) {
	if os.Getenv("LGMON_ROLE_HELPER") != "1" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		t.Fatal("missing helper arguments")
	}
	flags := flag.NewFlagSet("role-helper", flag.ContinueOnError)
	role := flags.String("role", "", "")
	home := flags.String("home", "", "")
	listen := flags.String("listen", "", "")
	monitor := flags.String("monitor", "", "")
	stop := flags.String("stop", "", "")
	port := flags.Int("port", 0, "")
	flags.String("nonce", "", "")
	if err := flags.Parse(os.Args[separator+1:]); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := os.Stat(*stop); err == nil {
					cancel()
					return
				}
			}
		}
	}()
	if *role == "demo" {
		listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", *port))
		if err != nil {
			t.Fatal(err)
		}
		server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "owned process") })}
		go func() { <-ctx.Done(); server.Close() }()
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			t.Fatal(err)
		}
		return
	}
	client := &runtimeClient{wire: make(chan feishu.CardMessage, 8), release: make(chan struct{})}
	close(client.release)
	err := RunLocal(ctx, LocalOptions{Home: *home, Role: *role, Listen: *listen, Monitor: *monitor, Version: "fake-integration", Host: roleHost{role: *role},
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)), NewClient: func(config.FeishuApp, []byte) (feishu.Client, error) { return remoteCardClient{client}, nil }})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTwoProcessesNativeCollectionFiringAndRecovery(t *testing.T) {
	root := t.TempDir()
	monitorHome, collectorHome := filepath.Join(root, "monitor"), filepath.Join(root, "collector")
	for _, home := range []string{monitorHome, collectorHome} {
		if err := os.MkdirAll(filepath.Join(home, "config"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	freePort := func() int {
		t.Helper()
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		return listener.Addr().(*net.TCPAddr).Port
	}
	demoPort, monitorPort := freePort(), freePort()
	for monitorPort == demoPort {
		monitorPort = freePort()
	}
	nonce := rand.Text()
	targets := fmt.Sprintf("targets:\n- id: own-process\n  match:\n    process: '%s'\n    cmdline: '--role\\s+demo\\s+--nonce\\s+%s(?:\\s|$)'\n    port: %d\n  probes:\n  - {type: http, url: 'http://127.0.0.1:%d/', interval: 2s, timeout: 1s}\n", filepath.Base(os.Args[0]), nonce, demoPort, demoPort)
	for _, home := range []string{monitorHome, collectorHome} {
		if err := os.WriteFile(filepath.Join(home, "config", "targets.yaml"), []byte(targets), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rules := "rules:\n- {id: demo-down, type: process_absent, target: own-process, for: 5s, severity: critical, notify: [fake-channel], template: detailed}\n"
	notifiers := "feishuApps:\n- {id: fake-app, appId: cli_fake_two_process, secretRef: fake-ref, domain: feishu}\nnotifiers:\n- {id: fake-channel, type: feishu_app, app: fake-app, chatId: oc_fake_two_process}\n"
	for name, data := range map[string]string{"rules.yaml": rules, "notifiers.yaml": notifiers} {
		if err := os.WriteFile(filepath.Join(monitorHome, "config", name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	vault, err := secrets.New(monitorHome, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.Set(context.Background(), "fake-ref", []byte("fake-two-process-secret")); err != nil {
		t.Fatal(err)
	}
	type ownProcess struct {
		cmd         *exec.Cmd
		done        chan struct{}
		stop        string
		output      bytes.Buffer
		err         error
		allowKilled bool
	}
	start := func(role string, args ...string) *ownProcess {
		t.Helper()
		process := &ownProcess{stop: filepath.Join(root, fmt.Sprintf("stop-%s-%d", role, time.Now().UnixNano())), done: make(chan struct{})}
		base := []string{"-test.run=^TestRemoteRoleProcess$", "--", "--role", role, "--nonce", nonce, "--stop", process.stop}
		process.cmd = exec.Command(os.Args[0], append(base, args...)...)
		process.cmd.Env = append(os.Environ(), "LGMON_ROLE_HELPER=1")
		process.cmd.Stdout, process.cmd.Stderr = &process.output, &process.output
		if err := process.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		go func() { process.err = process.cmd.Wait(); close(process.done) }()
		t.Cleanup(func() {
			if err := os.WriteFile(process.stop, []byte("stop"), 0o600); err != nil {
				t.Error(err)
			}
			select {
			case <-process.done:
			case <-time.After(5 * time.Second):
				process.cmd.Process.Kill()
				<-process.done
				t.Errorf("%s did not stop gracefully: %s", role, process.output.String())
			}
			if process.err != nil && !process.allowKilled {
				t.Errorf("%s exited with error: %v %s", role, process.err, process.output.String())
			}
		})
		return process
	}
	demo := start("demo", "--port", strconv.Itoa(demoPort))
	address := fmt.Sprintf("127.0.0.1:%d", monitorPort)
	monitor := start("monitor", "--home", monitorHome, "--listen", address)
	waitFor := func(label string, condition func() bool) {
		t.Helper()
		at := time.Now()
		for time.Since(at) < 15*time.Second {
			select {
			case <-monitor.done:
				t.Fatalf("monitor exited: %v %s", monitor.err, monitor.output.String())
			default:
			}
			if condition() {
				t.Logf("two-process %s: %s (fake Feishu)", label, time.Since(at))
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("two-process %s exceeded 15s", label)
	}
	waitFor("monitor ready", func() bool {
		connection, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err != nil {
			return false
		}
		connection.Close()
		return true
	})
	collector := start("collector", "--home", collectorHome, "--monitor", address)
	path := filepath.Join(monitorHome, "data", "lgmon.db")
	waitFor("up", func() bool {
		select {
		case <-collector.done:
			t.Fatalf("collector exited: %v %s", collector.err, collector.output.String())
		default:
		}
		view, err := store.OpenView(context.Background(), path)
		if err != nil {
			return false
		}
		defer view.Close()
		targets, err := view.CurrentTargets(context.Background())
		return err == nil && len(targets) == 1 && targets[0].State == "up"
	})
	demo.allowKilled = true
	if err := demo.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-demo.done
	alertID := ""
	waitState := func(state string) bool {
		view, err := store.OpenView(context.Background(), path)
		if err != nil {
			return false
		}
		defer view.Close()
		alerts, err := view.Alerts(context.Background(), true)
		if err != nil || len(alerts) != 1 || alerts[0].State != state || (alertID != "" && alerts[0].ID != alertID) {
			return false
		}
		alertID = alerts[0].ID
		receipt, err := view.Delivery(context.Background(), alertID, state, "fake-channel")
		return err == nil && receipt.OK
	}
	waitFor("firing and successful audit", func() bool { return waitState("firing") })
	start("demo", "--port", strconv.Itoa(demoPort))
	waitFor("resolved and successful audit", func() bool { return waitState("resolved") })
	if _, err := os.Stat(filepath.Join(collectorHome, "data", "lgmon.db")); !os.IsNotExist(err) {
		t.Fatal("collector wrote monitor database")
	}
	if _, err := os.Stat(filepath.Join(collectorHome, "data", "secrets.json")); !os.IsNotExist(err) {
		t.Fatal("collector received notification secrets")
	}
}
