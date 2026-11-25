// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"log/slog"
	"net"
	"slices"
	"sync"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/ports"
)

// portMoves remembers which ports Windows had given away this time, for the page to explain.
type portMoves struct {
	configured []int
	mu         sync.Mutex
	moved      []ports.Moved
}

// note records where a listener ended up; a configured port that works again is no longer a move.
func (m *portMoves) note(configured string, actual net.Addr, logger *slog.Logger) {
	move, moved := ports.Compare(configured, actual)
	m.mu.Lock()
	m.moved = slices.DeleteFunc(m.moved, func(item ports.Moved) bool { return item.Configured == ports.Number(configured) })
	if moved {
		m.moved = append(m.moved, move)
	}
	m.mu.Unlock()
	if moved {
		logger.Warn("Port is reserved by Windows; using another one for now", "configured", move.Configured, "actual", move.Actual)
	}
}

func (m *portMoves) notice() *api.GuidePorts {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.moved) == 0 {
		return nil
	}
	result := &api.GuidePorts{Fix: ports.Fix(slices.DeleteFunc(slices.Clone(m.configured), func(port int) bool { return port == 0 }))}
	for _, item := range m.moved {
		result.Moved = append(result.Moved, api.GuidePort{Configured: item.Configured, Actual: item.Actual})
	}
	return result
}
