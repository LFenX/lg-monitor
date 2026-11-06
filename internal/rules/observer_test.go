package rules

import (
	"context"
	"errors"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

type transitionObserver struct{ alerts []Alert }

func (o *transitionObserver) Transition(alert Alert, _ Event) { o.alerts = append(o.alerts, alert) }

func TestTransitionObserverSeesOnlyDurablySavedTransitions(t *testing.T) {
	f := newFixture(t, "0s", "process_absent", 0)
	observer := &transitionObserver{}
	opts := f.options()
	opts.Observer = observer
	engine, err := New(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	f.repository.saveErr = errors.New("fake storage failure")
	message := pipeline.Message{CollectorID: "fake", Seq: 1, TS: f.now.UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: collector.StateDown, Process: collector.ProcessMissing}}
	if err := engine.Handle(context.Background(), message); err == nil || len(observer.alerts) != 0 {
		t.Fatal("unsaved transition reached SSE observer")
	}
	f.repository.saveErr = nil
	if err := engine.Evaluate(context.Background()); err != nil || len(observer.alerts) != 1 || observer.alerts[0].State != StateFiring {
		t.Fatalf("saved transition was not observed once: %+v %v", observer.alerts, err)
	}
	if err := engine.Evaluate(context.Background()); err != nil || len(observer.alerts) != 1 {
		t.Fatal("stable state emitted another transition")
	}
}
