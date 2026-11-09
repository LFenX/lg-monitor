//go:build integration && windows

package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestNativeProcessEventCollectorHelper(t *testing.T) {
	if os.Getenv("LGMON_COLLECTOR_EVENT_HELPER") != "1" {
		return
	}
	fmt.Fprintf(os.Stdout, "ready %d\n", os.Getpid())
	_, _ = io.Copy(io.Discard, os.Stdin)
}

type nativeReadyProcessEvents struct {
	collector.ProcessEventSource
	ready chan struct{}
	once  sync.Once
}

func (s *nativeReadyProcessEvents) Run(ctx context.Context, filter collector.ProcessEventFilter, notify func(collector.ProcessEvent)) error {
	return s.ProcessEventSource.Run(ctx, filter, func(event collector.ProcessEvent) {
		if event.Kind == collector.ProcessEventsReady {
			s.once.Do(func() { close(s.ready) })
		}
		notify(event)
	})
}

func TestNativeProcessEventCollectorStateWithinOneSecond(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, executable, "-test.run=^TestNativeProcessEventCollectorHelper$")
	child.Env = append(os.Environ(), "LGMON_COLLECTOR_EVENT_HELPER=1")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	childDone := false
	defer func() {
		input.Close()
		if !childDone {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	line, err := bufio.NewReader(output).ReadString('\n')
	var actualPID int
	_, parsed := fmt.Sscanf(line, "ready %d\n", &actualPID)
	if err != nil || parsed != nil || actualPID != child.Process.Pid {
		t.Fatal("own child is not ready", line, err)
	}
	files := fstest.MapFS{"targets.yaml": {Data: []byte(fmt.Sprintf("targets:\n- id: own-child\n  match:\n    process: %q\n    cmdline: TestNativeProcessEventCollectorHelper\n", filepath.Base(executable)))}}
	files["rules.yaml"] = &fstest.MapFile{Data: []byte("rules:\n- {id: own-exit, type: process_absent, target: own-child, for: 0s, severity: critical, notify: [fake-channel]}\n")}
	files["notifiers.yaml"] = &fstest.MapFile{Data: []byte("feishuApps:\n- {id: fake-app, appId: cli_fake_event, secretRef: fake-ref, domain: feishu}\nnotifiers:\n- {id: fake-channel, type: feishu_app, app: fake-app, chatId: oc_fake_event}\n")}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager, err := config.NewManager(config.FSLoader{FS: files}, logger)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "event.db")
	db, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	inputMessages := pipeline.NewChannel(32)
	client := &runtimeClient{wire: make(chan feishu.CardMessage, 8), release: make(chan struct{})}
	close(client.release)
	runtime, err := NewRuntime(RuntimeOptions{ID: "own-event-node", Hostname: "fake-event-host", Config: manager, Store: db, Source: inputMessages, Secrets: runtimeVault{}, NewClient: func(config.FeishuApp, []byte) (feishu.Client, error) { return client, nil }, Now: time.Now, Logger: logger})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	runtimeDone := make(chan error, 1)
	go func() { runtimeDone <- runtime.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-runtimeDone:
			if err != nil {
				t.Error("runtime cleanup", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("runtime did not stop")
		}
	})
	view, err := store.OpenView(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	events := &nativeReadyProcessEvents{ProcessEventSource: collector.NewProcessEventSource(time.Now), ready: make(chan struct{})}
	statuses := make(chan pipeline.Message, 16)
	samples := make(chan struct{}, 16)
	prober := collector.NewProber(time.Now)
	defer prober.Close()
	instance, err := NewCollector(CollectorOptions{ID: "own-event-node", Config: manager, Processes: collector.NewProcessSource(time.Now, logger), ProcessEvents: events, Ports: collector.NewPortSource(time.Now, logger), Probes: prober, Sink: sinkFunc(func(ctx context.Context, message pipeline.Message) error {
		if err := inputMessages.Write(ctx, message); err != nil {
			return err
		}
		if message.Kind == pipeline.KindTargetStatus {
			select {
			case statuses <- message:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if message.Kind == pipeline.KindProcessSample {
			select {
			case samples <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}), Now: time.Now, Logger: logger, Interval: 30 * time.Second, SnapshotInterval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- instance.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error("collector cleanup", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("collector retained event subscription")
		}
	}()
	select {
	case state := <-statuses:
		if state.TargetStatus.State != collector.StateUp || len(state.TargetStatus.Processes) != 1 || state.TargetStatus.Processes[0].PID != int32(child.Process.Pid) {
			t.Fatal("initial own target is not up", state)
		}
	case <-ctx.Done():
		t.Fatal("no initial target state")
	}
	select {
	case <-events.ready:
	case <-ctx.Done():
		t.Fatal("actual event source not ready")
	}
	// Settle the initial and ready-triggered snapshots before requesting exit.
	// Otherwise the ready hint could mask a missing process-exit hint.
	for range 2 {
		select {
		case <-samples:
		case <-ctx.Done():
			t.Fatal("initial event reconciliation did not finish")
		}
	}
	started := time.Now()
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	childDone = true
	deadline := time.NewTimer(time.Second - time.Since(started))
	defer deadline.Stop()
	for {
		select {
		case message := <-statuses:
			if message.TargetStatus.State != collector.StateDown {
				continue
			}
			elapsed := time.Since(started)
			if elapsed >= time.Second || message.TS < started.UnixMilli() {
				t.Fatal("target state missed event deadline", elapsed, message)
			}
			t.Logf("native exit to validated target down: %s; regular sampling interval 30s", elapsed)
			persistDeadline := time.NewTimer(4 * time.Second)
			defer persistDeadline.Stop()
			check := time.NewTicker(20 * time.Millisecond)
			defer check.Stop()
			for {
				states, stateErr := view.CurrentTargets(ctx)
				alerts, alertErr := view.Alerts(ctx, false)
				if stateErr != nil || alertErr != nil {
					t.Fatal("read native event state", stateErr, alertErr)
				}
				if len(states) == 1 && states[0].State == "down" && states[0].TS == message.TS && len(alerts) == 1 && alerts[0].RuleID == "own-exit" && alerts[0].CollectorID == "own-event-node" && alerts[0].TargetID == "own-child" && alerts[0].StartedAt >= started.UnixMilli() && len(client.wire) == 1 {
					t.Logf("native target down persisted and process_absent fired with fake delivery after %s", time.Since(started))
					return
				}
				select {
				case <-check.C:
				case <-persistDeadline.C:
					t.Fatal("native event did not reach SQLite/rule/fake delivery", states, alerts, len(client.wire))
				}
			}
		case <-deadline.C:
			t.Fatal("target exit was not detected within one second without regular tick")
		case err := <-done:
			done <- err
			t.Fatal("collector stopped before event state", err)
		}
	}
}
