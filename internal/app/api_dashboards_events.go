package app

import (
	"context"
	"strings"

	"github.com/LFenX/lg-monitor/internal/config"
)

type dashboardChange struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Deleted bool   `json:"deleted,omitempty"`
}

func dashboardRevisions(cfg *config.Config) map[string]string {
	result := map[string]string{}
	for _, state := range cfg.DashboardStates {
		message := ""
		if state.Error != nil {
			message = *state.Error
		}
		result[state.ID] = state.Version + "\x00" + message
	}
	return result
}
func watchDashboardEvents(ctx context.Context, source RuntimeConfig, publisher LivePublisher) {
	previous := dashboardRevisions(source.Snapshot())
	updates := source.Subscribe(ctx)
	go func() {
		for cfg := range updates {
			current := dashboardRevisions(cfg)
			for id, revision := range current {
				if old, found := previous[id]; found && old == revision {
					continue
				}
				_ = publisher.Publish("dashboard", dashboardChange{ID: id, Version: strings.SplitN(revision, "\x00", 2)[0]})
			}
			for id, revision := range previous {
				if _, found := current[id]; !found {
					_ = publisher.Publish("dashboard", dashboardChange{ID: id, Version: strings.SplitN(revision, "\x00", 2)[0], Deleted: true})
				}
			}
			previous = current
		}
	}()
}
