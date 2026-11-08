package examples

import (
	"fmt"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

const WorkloadTargets = 50
const WorkloadProcessInterval = 2 * time.Second
const WorkloadStatusInterval = 10 * time.Second
const WorkloadSystemInterval = 5 * time.Second
const WorkloadHTTPInterval = 30 * time.Second
const WorkloadProcessRSS = uint64(32 * 1024 * 1024)
const WorkloadProcessCPU = 0.25
const WorkloadHTTPMilliseconds = int64(12)

type Workload struct {
	Collector string
	StartedAt time.Time
}

func WorkloadTargetID(index int) string { return fmt.Sprintf("scale-target-%02d", index) }

func WorkloadTarget(index int) config.Target {
	return config.Target{ID: WorkloadTargetID(index), Match: config.Match{Process: fmt.Sprintf("fake-node-%02d.exe", index)},
		Probes: []config.Probe{{Type: "http", URL: fmt.Sprintf("http://127.0.0.1:18080/fake/%d", index), Interval: "30s", Timeout: "2s"}}}
}

func (w Workload) Status(index int, at time.Time, down bool) pipeline.Message {
	id := WorkloadTargetID(index)
	state, process, reason := collector.StateUp, collector.ProcessFound, "matched process"
	var processes []pipeline.ProcessRef
	if down {
		state, process, reason = collector.StateDown, collector.ProcessMissing, "no matching process"
	} else {
		processes = []pipeline.ProcessRef{{
			PID: int32(1000 + index), ParentPID: 1, Name: fmt.Sprintf("fake-node-%02d.exe", index),
			Exe:       fmt.Sprintf(`C:\apps\fixture\fake-node-%02d.exe`, index),
			CreatedAt: w.StartedAt.Add(-time.Hour).UnixMilli(), CPUPercent: WorkloadProcessCPU, RSS: WorkloadProcessRSS, Threads: 8,
		}}
	}
	return pipeline.Message{CollectorID: w.Collector, Seq: 1, TS: at.UnixMilli(), Kind: pipeline.KindTargetStatus,
		TargetStatus: &pipeline.TargetStatus{TargetID: id, State: state, Process: process, Reason: reason, Full: true, Processes: processes, Ports: []collector.Port{}}}
}

func (w Workload) Messages(elapsed time.Duration, at time.Time, down func(int) bool) []pipeline.Message {
	var messages []pipeline.Message
	for index := 0; index < WorkloadTargets; index++ {
		unavailable := down != nil && down(index)
		if elapsed%WorkloadProcessInterval == 0 && !unavailable {
			messages = append(messages, pipeline.Message{CollectorID: w.Collector, Seq: 1, TS: at.UnixMilli(), Kind: pipeline.KindProcessSample,
				ProcessSample: &pipeline.ProcessSample{TargetID: WorkloadTargetID(index), PID: int32(1000 + index), CreatedAt: w.StartedAt.Add(-time.Hour).UnixMilli(), CPUPercent: WorkloadProcessCPU, RSS: WorkloadProcessRSS}})
		}
		if elapsed%WorkloadStatusInterval == 0 {
			messages = append(messages, w.Status(index, at, unavailable))
		}
		if elapsed%WorkloadHTTPInterval == 0 {
			result := pipeline.ProbeResult{TargetID: WorkloadTargetID(index), Probe: 0, TS: at.UnixMilli(), Type: "http", OK: !unavailable, StatusCode: 200, LatencyMS: WorkloadHTTPMilliseconds}
			if unavailable {
				result.StatusCode, result.Error = 503, "HTTP status 503"
			}
			messages = append(messages, pipeline.Message{CollectorID: w.Collector, Seq: 1, TS: at.UnixMilli(), Kind: pipeline.KindProbeResult, ProbeResult: &result})
		}
	}
	if elapsed%WorkloadSystemInterval == 0 {
		messages = append(messages, pipeline.Message{CollectorID: w.Collector, Seq: 1, TS: at.UnixMilli(), Kind: pipeline.KindSystemMetrics,
			SystemMetrics: &collector.SystemMetrics{TS: at.UnixMilli(), CPUPercent: 1.5, MemoryTotal: 16 << 30, MemoryUsed: 2 << 30, MemoryAvailable: 14 << 30, MemoryPercent: 12.5, UptimeSeconds: 3600 + uint64(elapsed/time.Second),
				Disks:    []collector.DiskMetrics{{Path: `C:\`, Total: 1 << 40, Used: 1 << 39, Free: 1 << 39, UsedPercent: 50}},
				Networks: []collector.NetworkMetrics{{Name: "fake-network", BytesSent: 1000000 + uint64(elapsed/time.Second)*100, BytesReceived: 2000000 + uint64(elapsed/time.Second)*200, SendBytesPerSec: 100, ReceiveBytesPerSec: 200}},
			}})
	}
	return messages
}
