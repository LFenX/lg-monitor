package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
)

type recordedLiveEvents struct {
	names []string
	data  []json.RawMessage
}

func (r *recordedLiveEvents) Publish(name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	r.names = append(r.names, name)
	r.data = append(r.data, data)
	return nil
}

func TestLiveEventsPublishSamplesAndOnlyChangedTargetFacts(t *testing.T) {
	recorder := &recordedLiveEvents{}
	observer := newAPIEvents(recorder, slog.New(slog.NewTextHandler(io.Discard, nil)))
	message := pipeline.Message{CollectorID: "fake", Seq: 1, TS: 1, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: "up", Process: collector.ProcessFound}}
	observer.Sample(message)
	message.TS++
	observer.Sample(message)
	message.TargetStatus.State = "down"
	message.TargetStatus.Process = collector.ProcessMissing
	observer.Sample(message)
	if len(recorder.names) != 5 || recorder.names[0] != "sample" || recorder.names[1] != "target" || recorder.names[2] != "sample" || recorder.names[4] != "target" {
		t.Fatalf("live event kinds: %+v", recorder.names)
	}
	if string(recorder.data[1]) == string(recorder.data[4]) {
		t.Fatal("changed target state did not reach its event")
	}
}

type runtimeEventChannels struct {
	samples chan pipeline.Message
	alerts  chan rules.Alert
}

func (o runtimeEventChannels) Sample(message pipeline.Message)             { o.samples <- message }
func (o runtimeEventChannels) Transition(alert rules.Alert, _ rules.Event) { o.alerts <- alert }

func TestRuntimeConnectsSamplesAndCommittedAlertsToObserver(t *testing.T) {
	f := newRuntimeFixture(t)
	source := pipeline.NewChannel(4)
	f.opts.Source = source
	observer := runtimeEventChannels{samples: make(chan pipeline.Message, 4), alerts: make(chan rules.Alert, 4)}
	f.opts.Observer = observer
	ready := make(chan struct{}, 1)
	f.opts.Ready = func() { ready <- struct{}{} }
	runtime, err := NewRuntime(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := receiveRuntime(t, done); err != nil {
			t.Error(err)
		}
	})
	receiveRuntime(t, ready)
	message := pipeline.Message{CollectorID: f.opts.ID, Seq: 1, TS: f.clock.Now().UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "demo", State: collector.StateDown, Process: collector.ProcessMissing}}
	if err := source.Write(ctx, message); err != nil {
		t.Fatal(err)
	}
	if got := receiveRuntime(t, observer.samples); got.TargetStatus.State != collector.StateDown {
		t.Fatal("runtime observer did not receive the sample")
	}
	alert := receiveRuntime(t, observer.alerts)
	active, err := f.db.ActiveAlerts(context.Background())
	if err != nil || len(active) != 1 || active[0].ID != alert.ID {
		t.Fatalf("observer saw an alert before persistence: %+v %v", active, err)
	}
}
