// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package license

import "context"

// UsageSnapshot captures anonymous local adoption milestones (no service names, URLs, or secrets)
// reported alongside license checks so the author's admin growth dashboard can render conversion funnels.
type UsageSnapshot struct {
	BoardsCount     int      `json:"boardsCount"`
	MonitorsCount   int      `json:"monitorsCount"`
	HasFirstBoard   bool     `json:"hasFirstBoard"`
	HasFirstMonitor bool     `json:"hasFirstMonitor"`
	Features        []string `json:"features,omitempty"`
}

// SetUsage attaches the local usage provider after backend initialization.
func (m *Manager) SetUsage(fn func() UsageSnapshot) {
	m.mu.Lock()
	m.usage = fn
	m.mu.Unlock()
}

// LocalUsage returns the current anonymous usage snapshot for this installation.
func (m *Manager) LocalUsage() UsageSnapshot {
	m.mu.Lock()
	fn := m.usage
	m.mu.Unlock()
	if fn == nil {
		return UsageSnapshot{}
	}
	return fn()
}

// reportTelemetry best-effort posts anonymous milestone usage to the license server when available.
// Older license servers that do not expose /v1/telemetry return 404 and are ignored quietly.
func (m *Manager) reportTelemetry(ctx context.Context, token string, usage UsageSnapshot) {
	if token == "" {
		return
	}
	_, _, _ = m.client.Post(ctx, "/v1/telemetry", "", map[string]any{
		"token":  token,
		"device": m.device(),
		"usage":  usage,
	})
}
