package collector

import (
	"context"
	"log/slog"
	"time"
)

const (
	StateUp      = "up"
	StateDown    = "down"
	StateUnknown = "unknown"
)

type ProcessSource interface {
	Snapshot(context.Context) ([]Process, error)
}

type PortSource interface {
	ListeningTCP(context.Context) ([]Port, error)
}

type ProcessIdentity struct {
	PID       int32 `json:"pid"`
	CreatedAt int64 `json:"createdAt"`
}

type Process struct {
	PID                int32   `json:"pid"`
	ParentPID          int32   `json:"parentPid"`
	Name               string  `json:"name"`
	Cmdline            string  `json:"cmdline"`
	Exe                string  `json:"exe"`
	CreatedAt          int64   `json:"createdAt"`
	CPUPercent         float64 `json:"cpuPercent"`
	RSS                uint64  `json:"rss"`
	Threads            int32   `json:"threads"`
	CmdlineUnavailable bool    `json:"cmdlineUnavailable,omitempty"`
}

func (p Process) Identity() ProcessIdentity {
	return ProcessIdentity{PID: p.PID, CreatedAt: p.CreatedAt}
}

type Port struct {
	LocalAddress string `json:"localAddress"`
	Port         uint32 `json:"port"`
	PID          int32  `json:"pid"`
}

type instrumentation struct {
	now    func() time.Time
	logger *slog.Logger
}

func newInstrumentation(now func() time.Time, logger *slog.Logger) instrumentation {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return instrumentation{now: now, logger: logger}
}

func (i instrumentation) finish(kind string, started time.Time) {
	if elapsed := i.now().Sub(started); elapsed > time.Second {
		i.logger.Warn("Collection took longer than one second", "source", kind, "durationMs", elapsed.Milliseconds())
	}
}
