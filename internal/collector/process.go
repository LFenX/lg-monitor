package collector

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	gprocess "github.com/shirou/gopsutil/v4/process"
)

type processReader interface {
	PpidWithContext(context.Context) (int32, error)
	NameWithContext(context.Context) (string, error)
	CmdlineWithContext(context.Context) (string, error)
	ExeWithContext(context.Context) (string, error)
	CreateTimeWithContext(context.Context) (int64, error)
	TimesWithContext(context.Context) (*cpu.TimesStat, error)
	MemoryInfoWithContext(context.Context) (*gprocess.MemoryInfoStat, error)
	NumThreadsWithContext(context.Context) (int32, error)
}

type processEntry struct {
	pid    int32
	reader processReader
}

type processBackend interface {
	Processes(context.Context) ([]processEntry, error)
	LogicalProcessors() uint32
}

type nativeProcesses struct{}

type cpuSample struct {
	seconds float64
	at      time.Time
}

type processDetails struct {
	cmdline            string
	exe                string
	cmdlineUnavailable bool
}

type processSource struct {
	instrumentation
	backend  processBackend
	mu       sync.Mutex
	previous map[ProcessIdentity]cpuSample
	details  map[ProcessIdentity]processDetails
}

func NewProcessSource(now func() time.Time, logger *slog.Logger) ProcessSource {
	return newProcessSource(nativeProcesses{}, now, logger)
}

func newProcessSource(backend processBackend, now func() time.Time, logger *slog.Logger) *processSource {
	return &processSource{instrumentation: newInstrumentation(now, logger), backend: backend, previous: make(map[ProcessIdentity]cpuSample), details: make(map[ProcessIdentity]processDetails)}
}

func (s *processSource) Snapshot(ctx context.Context) ([]Process, error) {
	started := s.now()
	defer s.finish("process", started)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("进程采集已取消：%w", err)
	}
	entries, err := s.backend.Processes(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取进程列表失败，请检查当前账户的进程读取权限：%w", err)
	}
	processors := s.backend.LogicalProcessors()
	if processors == 0 {
		return nil, fmt.Errorf("无法读取逻辑处理器数量，请检查系统处理器信息")
	}
	result := make([]Process, 0, len(entries))
	next := make(map[ProcessIdentity]cpuSample, len(entries))
	nextDetails := make(map[ProcessIdentity]processDetails, len(entries))
	skipped := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("进程采集已取消：%w", err)
		}
		createdAt, err := entry.reader.CreateTimeWithContext(ctx)
		if entry.pid == 0 || err != nil || createdAt <= 0 {
			// PID alone cannot identify a process safely after it has been reused.
			skipped++
			continue
		}
		row := Process{PID: entry.pid, CreatedAt: createdAt}
		row.ParentPID = available(entry.reader.PpidWithContext(ctx))
		row.Name = available(entry.reader.NameWithContext(ctx))
		row.Threads = available(entry.reader.NumThreadsWithContext(ctx))
		if memory := available(entry.reader.MemoryInfoWithContext(ctx)); memory != nil {
			row.RSS = memory.RSS
		}
		key := row.Identity()
		details, cached := s.details[key]
		if !cached {
			cmdline, err := entry.reader.CmdlineWithContext(ctx)
			details = processDetails{cmdline: available(cmdline, err), exe: available(entry.reader.ExeWithContext(ctx)), cmdlineUnavailable: err != nil}
		}
		nextDetails[key] = details
		row.Cmdline, row.Exe, row.CmdlineUnavailable = details.cmdline, details.exe, details.cmdlineUnavailable
		// A temporary read failure must not discard the last successful CPU baseline.
		if previous, ok := s.previous[key]; ok {
			next[key] = previous
		}
		if times := available(entry.reader.TimesWithContext(ctx)); times != nil {
			total := times.User + times.System
			if total >= 0 && !math.IsNaN(total) && !math.IsInf(total, 0) {
				current := cpuSample{seconds: total, at: s.now()}
				if previous, ok := s.previous[key]; ok {
					elapsed, used := current.at.Sub(previous.at).Seconds(), current.seconds-previous.seconds
					if elapsed > 0 && used >= 0 {
						row.CPUPercent = min(used/elapsed/float64(processors)*100, 100)
					}
				}
				next[key] = current
			}
		}
		result = append(result, row)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("进程采集已取消：%w", err)
	}
	s.previous = next
	s.details = nextDetails
	slices.SortFunc(result, func(a, b Process) int {
		if order := cmp.Compare(a.PID, b.PID); order != 0 {
			return order
		}
		return cmp.Compare(a.CreatedAt, b.CreatedAt)
	})
	if skipped > 0 {
		s.logger.Debug("Skipped processes with unavailable creation times", "count", skipped)
	}
	return result, nil
}

func filetimeToUnixMillis(ticks int64) int64 {
	return ticks/10000 - 11644473600000
}

func available[T any](value T, err error) T {
	if err != nil {
		var zero T
		return zero
	}
	return value
}
