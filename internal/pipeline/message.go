package pipeline

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
)

type Kind string

const (
	KindTargetStatus   Kind = "targetStatus"
	KindProbeResult    Kind = "probeResult"
	KindProcessSample  Kind = "processSample"
	KindDataGap        Kind = "dataGap"
	KindSystemMetrics  Kind = "systemMetrics"
	KindServiceStatus  Kind = "serviceStatus"
	KindEventLog       Kind = "eventLog"
	KindEventLogNotice Kind = "eventLogNotice"
	KindInventory      Kind = "inventory"
)

const MaxSequence = uint64(math.MaxInt64)

type DataGap struct {
	FromSeq uint64 `json:"fromSeq"`
	ToSeq   uint64 `json:"toSeq"`
	Dropped uint64 `json:"dropped"`
	Reason  string `json:"reason"`
}

type TargetStatus struct {
	TargetID    string           `json:"targetId"`
	State       string           `json:"state"`
	Process     string           `json:"process"`
	Port        string           `json:"port"`
	Reason      string           `json:"reason"`
	Service     string           `json:"service,omitempty"`
	ServiceName string           `json:"serviceName,omitempty"`
	Full        bool             `json:"full"`
	Processes   []ProcessRef     `json:"processes"`
	Ports       []collector.Port `json:"ports"`
}

type ProcessRef struct {
	PID        int32   `json:"pid"`
	ParentPID  int32   `json:"parentPid"`
	Name       string  `json:"name"`
	Exe        string  `json:"exe"`
	CreatedAt  int64   `json:"createdAt"`
	CPUPercent float64 `json:"cpuPercent"`
	RSS        uint64  `json:"rss"`
	Threads    int32   `json:"threads"`
}

func Ref(process collector.Process) ProcessRef {
	return ProcessRef{PID: process.PID, ParentPID: process.ParentPID, Name: process.Name, Exe: process.Exe, CreatedAt: process.CreatedAt, CPUPercent: process.CPUPercent, RSS: process.RSS, Threads: process.Threads}
}

type ProbeResult = collector.ProbeResult

type ProcessSample struct {
	IntervalMillis int64   `json:"intervalMillis,omitempty"`
	TargetID       string  `json:"targetId"`
	PID            int32   `json:"pid"`
	CreatedAt      int64   `json:"createdAt"`
	CPUPercent     float64 `json:"cpuPercent"`
	RSS            uint64  `json:"rss"`
}

type Message struct {
	CollectorID    string                       `json:"collectorId"`
	Seq            uint64                       `json:"seq"`
	TS             int64                        `json:"ts"`
	Kind           Kind                         `json:"kind"`
	TargetStatus   *TargetStatus                `json:"targetStatus,omitempty"`
	ProbeResult    *ProbeResult                 `json:"probeResult,omitempty"`
	ProcessSample  *ProcessSample               `json:"processSample,omitempty"`
	DataGap        *DataGap                     `json:"dataGap,omitempty"`
	SystemMetrics  *collector.SystemMetrics     `json:"systemMetrics,omitempty"`
	ServiceStatus  *collector.Service           `json:"serviceStatus,omitempty"`
	EventLog       *collector.EventLogRecord    `json:"eventLog,omitempty"`
	EventLogNotice *collector.EventLogNotice    `json:"eventLogNotice,omitempty"`
	Inventory      *collector.InventorySnapshot `json:"inventory,omitempty"`
}

func (m Message) Validate() error {
	if m.CollectorID == "" || m.Seq == 0 || m.Seq > MaxSequence {
		return fmt.Errorf("采集消息缺少采集器 ID 或递增序号")
	}
	count := 0
	if m.TargetStatus != nil {
		count++
	}
	if m.ProbeResult != nil {
		count++
	}
	if m.ProcessSample != nil {
		count++
	}
	if m.DataGap != nil {
		count++
	}
	if m.SystemMetrics != nil {
		count++
	}
	if m.ServiceStatus != nil {
		count++
	}
	if m.EventLog != nil {
		count++
	}
	if m.EventLogNotice != nil {
		count++
	}
	if m.Inventory != nil {
		count++
	}
	if count != 1 {
		return fmt.Errorf("采集消息必须且只能包含一种数据")
	}
	switch m.Kind {
	case KindTargetStatus:
		if m.TargetStatus != nil && m.TargetStatus.TargetID != "" {
			if m.TargetStatus.Service != "" || m.TargetStatus.ServiceName != "" {
				fact := collector.Service{Name: m.TargetStatus.ServiceName, State: m.TargetStatus.Service, StartType: "unknown"}
				if err := fact.Validate(); err != nil {
					return fmt.Errorf("服务分项事实无效：%w", err)
				}
			}
			switch m.TargetStatus.Process {
			case "", collector.ProcessFound, collector.ProcessMissing, collector.ProcessUnknown:
			default:
				return fmt.Errorf("进程分项事实必须为 found、missing、unknown 或空")
			}
			switch m.TargetStatus.Port {
			case "", collector.PortListening, collector.PortClosed, collector.PortUnknown:
			default:
				return fmt.Errorf("端口分项事实必须为 listening、closed、unknown 或空")
			}
			switch m.TargetStatus.State {
			case collector.StateUp, collector.StateDown, collector.StateUnknown:
				return nil
			}
		}
	case KindProbeResult:
		if m.ProbeResult != nil && m.ProbeResult.TargetID != "" && m.ProbeResult.Probe >= 0 {
			return m.ProbeResult.Validate()
		}
	case KindProcessSample:
		if m.ProcessSample != nil && m.ProcessSample.TargetID != "" && m.ProcessSample.PID > 0 && m.ProcessSample.CreatedAt > 0 {
			p := m.ProcessSample
			if p.IntervalMillis < 0 || p.IntervalMillis > 86400000 {
				return fmt.Errorf("进程采样周期必须为 0～86400000 毫秒，旧端省略时为 2000 毫秒")
			}
			if math.IsNaN(p.CPUPercent) || math.IsInf(p.CPUPercent, 0) || p.CPUPercent < 0 || p.CPUPercent > 100 || p.RSS > math.MaxInt64 {
				return fmt.Errorf("进程 CPU 必须在 0～100%% 之间，RSS 不能超过有符号 64 位整数范围")
			}
			return nil
		}
	case KindDataGap:
		if gap := m.DataGap; gap != nil && gap.FromSeq > 0 && gap.FromSeq <= gap.ToSeq && gap.ToSeq == m.Seq && gap.Dropped == gap.ToSeq-gap.FromSeq+1 && gap.Reason != "" {
			return nil
		}
	case KindSystemMetrics:
		if m.SystemMetrics != nil {
			return m.SystemMetrics.Validate()
		}
	case KindServiceStatus:
		if m.ServiceStatus != nil {
			return m.ServiceStatus.Validate()
		}
	case KindEventLog:
		if m.EventLog != nil && m.EventLog.TS == m.TS && len(m.EventLog.Presets)+len(m.EventLog.Targets) > 0 {
			return m.EventLog.Validate()
		}
	case KindEventLogNotice:
		if m.EventLogNotice != nil {
			return m.EventLogNotice.Validate()
		}
	case KindInventory:
		if m.Inventory != nil {
			return m.Inventory.Validate()
		}
	}
	return fmt.Errorf("采集消息类型与数据不符，或目标、状态、进程身份无效")
}

func (m Message) clone() Message {
	if m.TargetStatus != nil {
		copy := *m.TargetStatus
		copy.Processes, copy.Ports = slices.Clone(copy.Processes), slices.Clone(copy.Ports)
		m.TargetStatus = &copy
	}
	if m.ProbeResult != nil {
		copy := *m.ProbeResult
		copy.Metrics = maps.Clone(copy.Metrics)
		m.ProbeResult = &copy
	}
	if m.ProcessSample != nil {
		copy := *m.ProcessSample
		m.ProcessSample = &copy
	}
	if m.DataGap != nil {
		copy := *m.DataGap
		m.DataGap = &copy
	}
	if m.SystemMetrics != nil {
		copy := *m.SystemMetrics
		copy.Disks, copy.Networks, copy.Errors = slices.Clone(copy.Disks), slices.Clone(copy.Networks), slices.Clone(copy.Errors)
		m.SystemMetrics = &copy
	}
	if m.ServiceStatus != nil {
		copy := *m.ServiceStatus
		m.ServiceStatus = &copy
	}
	if m.EventLog != nil {
		copy := *m.EventLog
		copy.Presets, copy.Targets = slices.Clone(copy.Presets), slices.Clone(copy.Targets)
		m.EventLog = &copy
	}
	if m.EventLogNotice != nil {
		copy := *m.EventLogNotice
		m.EventLogNotice = &copy
	}
	if m.Inventory != nil {
		copy := *m.Inventory
		copy.Entries = slices.Clone(copy.Entries)
		m.Inventory = &copy
	}
	return m
}

type Sink interface {
	Write(context.Context, Message) error
}
type Source interface {
	Read(context.Context) (Message, error)
}

type Emitter struct {
	id   string
	sink Sink
	now  func() time.Time
	mu   sync.Mutex
	seq  uint64
}

func NewEmitter(id string, sink Sink, now func() time.Time) (*Emitter, error) {
	if id == "" || sink == nil || now == nil {
		return nil, fmt.Errorf("采集消息发送器需要 ID、接收端和时钟")
	}
	return &Emitter{id: id, sink: sink, now: now}, nil
}

func (e *Emitter) Emit(ctx context.Context, message Message) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("采集消息发送已取消：%w", err)
	}
	if e.seq >= MaxSequence {
		return fmt.Errorf("采集消息序号已耗尽")
	}
	message.CollectorID, message.Seq = e.id, e.seq+1
	if message.TS == 0 {
		message.TS = e.now().UTC().UnixMilli()
	}
	if err := message.Validate(); err != nil {
		return err
	}
	// Serializing writes preserves sequence order even when probes finish concurrently.
	if err := e.sink.Write(ctx, message); err != nil {
		return fmt.Errorf("发送采集消息失败：%w", err)
	}
	e.seq = message.Seq
	return nil
}
