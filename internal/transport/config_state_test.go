package transport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
)

type configStateTicker struct{ ticks chan time.Time }

func (t *configStateTicker) Ticks() <-chan time.Time { return t.ticks }
func (*configStateTicker) Stop()                     {}

type configStateClock struct{ created chan *configStateTicker }

func (c configStateClock) NewTicker(time.Duration) collector.ProbeTicker {
	ticker := &configStateTicker{ticks: make(chan time.Time)}
	c.created <- ticker
	return ticker
}

func waitConfigState(t *testing.T, ctx context.Context, receiver *Receiver, id, state string) CollectorConfigState {
	t.Helper()
	for {
		current := receiver.ConfigState(id)
		if current.State == state {
			return current
		}
		select {
		case <-ctx.Done():
			t.Fatalf("configuration state %s not reached: %+v", state, current)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestConfigurationCapabilitiesStopUnsupportedVersionsWithoutAffectingOtherCollectors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{Targets: []config.Target{
		{ID: "directory", Collector: "legacy", Match: config.Match{Process: "node.exe", WorkDir: `D:\site`}},
		{ID: "plain", Collector: "other", Match: config.Match{Process: "ping.exe"}},
	}}
	manager, err := config.NewManager(initialTargets{cfg}, logger)
	if err != nil {
		t.Fatal(err)
	}
	receiver := NewReceiver(nil)
	clock := configStateClock{created: make(chan *configStateTicker, 2)}
	receiver.SetConfigDelivery(ConfigDelivery{Source: manager, Clock: clock, Logger: logger})
	legacy := &linkSession{identity: Hello{CollectorID: "legacy"}}
	other := &linkSession{identity: Hello{CollectorID: "other"}}
	receiver.active["legacy"], receiver.active["other"] = legacy, other
	var sends atomic.Int32
	done := make(chan error, 2)
	go func() {
		done <- receiver.pushConfig(ctx, legacy, func(*lgmonv1.MonitorMessage) error { sends.Add(1); return nil }, make(chan *lgmonv1.ConfigApplied))
	}()
	ticker := <-clock.created
	state := waitConfigState(t, ctx, receiver, "legacy", "outdated")
	version, _, _ := config.TargetConfig(cfg, "legacy", "")
	if state.Version != version || state.Message == "" || sends.Load() != 0 {
		t.Fatalf("unsupported config sent or not explained: %+v", state)
	}
	otherSent := make(chan *lgmonv1.MonitorMessage, 1)
	otherApplied := make(chan *lgmonv1.ConfigApplied)
	go func() {
		done <- receiver.pushConfig(ctx, other, func(message *lgmonv1.MonitorMessage) error { otherSent <- message; return nil }, otherApplied)
	}()
	<-clock.created
	var message *lgmonv1.MonitorMessage
	select {
	case message = <-otherSent:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	pushed := message.GetConfig()
	if pushed == nil || receiver.ConfigState("other").State != "pending" {
		t.Fatal("unrelated collector did not receive ordinary config")
	}
	otherApplied <- &lgmonv1.ConfigApplied{Version: pushed.Version, Ok: true}
	waitConfigState(t, ctx, receiver, "other", "applied")
	ticker.ticks <- time.Time{}
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("configuration loops did not stop")
		}
	}
	if sends.Load() != 0 {
		t.Fatal("unsupported config retried after a tick")
	}
	supported := Hello{CollectorID: "legacy", Capabilities: []string{MatchDirectoryCapability}}
	if message := missingConfigCapability(cfg, supported); message != "" {
		t.Fatal(message)
	}
}

type controlledConfigApply struct{ calls chan configApplyCall }
type configApplyCall struct {
	version string
	result  chan error
}

func (a controlledConfigApply) Apply(ctx context.Context, version string, _ []byte) error {
	call := configApplyCall{version: version, result: make(chan error)}
	select {
	case a.calls <- call:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-call.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestConfigurationStatusFollowsRealStreamPendingRejectedAndApplied(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager, err := config.NewManager(initialTargets{testTargetConfig("baseline")}, logger)
	if err != nil {
		t.Fatal(err)
	}
	clock := &configClock{created: make(chan *configTicker, 2)}
	receiver.SetConfigDelivery(ConfigDelivery{Source: manager, Clock: clock, Logger: logger})
	applier := controlledConfigApply{calls: make(chan configApplyCall)}
	writer, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector"}, applier)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ticker := <-clock.created
	var first configApplyCall
	select {
	case first = <-applier.calls:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if state := receiver.ConfigState("fake-collector"); state.State != "pending" || state.Version != first.version {
		t.Fatalf("sent but unconfirmed config reported applied: %+v", state)
	}
	first.result <- errors.New("fake cache denied")
	if state := waitConfigState(t, ctx, receiver, "fake-collector", "rejected"); state.Message != "配置校验或保存失败，已保留旧配置" {
		t.Fatalf("collector rejection was lost: %+v", state)
	}
	ticker.ticks <- time.Time{}
	select {
	case retry := <-applier.calls:
		if retry.version != first.version {
			t.Fatal("retry changed content version")
		}
		retry.result <- nil
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waitConfigState(t, ctx, receiver, "fake-collector", "applied")
	receiver.mu.Lock()
	old := receiver.active["fake-collector"]
	replacement := &linkSession{identity: Hello{CollectorID: "fake-collector"}}
	receiver.active["fake-collector"] = replacement
	receiver.mu.Unlock()
	receiver.setConfigState(old, CollectorConfigState{State: "rejected", Message: "stale"})
	if state := receiver.ConfigState("fake-collector"); state.State != "applied" {
		t.Fatal("older session overwrote newer configuration status")
	}
	receiver.mu.Lock()
	receiver.active["fake-collector"] = old
	receiver.mu.Unlock()
}
