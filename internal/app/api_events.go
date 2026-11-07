package app

import (
	"log/slog"
	"sync"

	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

type LivePublisher interface{ Publish(string, any) error }

type apiEvents struct {
	publisher LivePublisher
	logger    *slog.Logger
	mu        sync.Mutex
	states    map[targetKey]targetEventState
}
type targetEventState struct{ state, reason, process, port, service string }

func (e *apiEvents) Pause(state store.NotificationPause) { e.publish("pause", state) }

func newAPIEvents(publisher LivePublisher, logger *slog.Logger) *apiEvents {
	return &apiEvents{publisher: publisher, logger: logger, states: map[targetKey]targetEventState{}}
}

func (e *apiEvents) publish(name string, value any) {
	if err := e.publisher.Publish(name, value); err != nil {
		e.logger.Warn("Live event was not published", "kind", name)
	}
}

func (e *apiEvents) Sample(message pipeline.Message) {
	e.publish("sample", message)
	if status := message.TargetStatus; status != nil {
		state := targetEventState{status.State, status.Reason, status.Process, status.Port, status.Service}
		key := targetKey{message.CollectorID, status.TargetID}
		e.mu.Lock()
		previous, exists := e.states[key]
		e.states[key] = state
		e.mu.Unlock()
		if !exists || previous != state {
			e.publish("target", message)
		}
	}
}

func (e *apiEvents) Transition(alert rules.Alert, event rules.Event) {
	e.publish("alert", struct {
		Alert rules.Alert `json:"alert"`
		Event rules.Event `json:"event"`
	}{alert, event})
}
