package collector

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
)

type DiskMetrics struct {
	Path        string  `json:"path"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"usedPercent"`
}

type NetworkMetrics struct {
	Name               string  `json:"name"`
	BytesSent          uint64  `json:"bytesSent"`
	BytesReceived      uint64  `json:"bytesReceived"`
	SendBytesPerSec    float64 `json:"sendBytesPerSec"`
	ReceiveBytesPerSec float64 `json:"receiveBytesPerSec"`
}

type SystemMetrics struct {
	TS              int64            `json:"ts"`
	CPUPercent      float64          `json:"cpuPercent"`
	MemoryTotal     uint64           `json:"memoryTotal"`
	MemoryUsed      uint64           `json:"memoryUsed"`
	MemoryAvailable uint64           `json:"memoryAvailable"`
	MemoryPercent   float64          `json:"memoryPercent"`
	UptimeSeconds   uint64           `json:"uptimeSeconds"`
	Disks           []DiskMetrics    `json:"disks"`
	Networks        []NetworkMetrics `json:"networks"`
	Errors          []string         `json:"errors,omitempty"`
}

type MetricsSource interface {
	Snapshot(context.Context) (SystemMetrics, error)
}

type rawSystemMetrics struct {
	SystemMetrics
	cpuTotal, cpuIdle float64
}
type metricsBackend interface {
	Read(context.Context) (rawSystemMetrics, error)
}
type nativeMetrics struct{}

type metricsSource struct {
	instrumentation
	backend    metricsBackend
	mu         sync.Mutex
	previous   *rawSystemMetrics
	previousAt time.Time
}

func NewMetricsSource(now func() time.Time, logger *slog.Logger) MetricsSource {
	return newMetricsSource(nativeMetrics{}, now, logger)
}
func newMetricsSource(backend metricsBackend, now func() time.Time, logger *slog.Logger) *metricsSource {
	return &metricsSource{instrumentation: newInstrumentation(now, logger), backend: backend}
}

func (s *metricsSource) Snapshot(ctx context.Context) (SystemMetrics, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	started := s.now()
	defer s.finish("system metrics", started)
	if err := ctx.Err(); err != nil {
		return SystemMetrics{}, fmt.Errorf("系统指标采集已取消：%w", err)
	}
	raw, err := s.backend.Read(ctx)
	if err != nil {
		return SystemMetrics{}, fmt.Errorf("读取系统指标失败：%w", err)
	}
	if math.IsNaN(raw.cpuTotal) || math.IsNaN(raw.cpuIdle) || math.IsInf(raw.cpuTotal, 0) || math.IsInf(raw.cpuIdle, 0) || raw.cpuTotal < 0 || raw.cpuIdle < 0 {
		return SystemMetrics{}, fmt.Errorf("CPU 累计时间无效")
	}
	raw.Disks, raw.Networks, raw.Errors = slices.Clone(raw.Disks), slices.Clone(raw.Networks), slices.Clone(raw.Errors)
	at := s.now()
	raw.TS, raw.CPUPercent = at.UTC().UnixMilli(), 0
	for i := range raw.Networks {
		raw.Networks[i].SendBytesPerSec, raw.Networks[i].ReceiveBytesPerSec = 0, 0
	}
	if old := s.previous; old != nil && at.After(s.previousAt) {
		total, idle := raw.cpuTotal-old.cpuTotal, raw.cpuIdle-old.cpuIdle
		if total > 0 && idle >= 0 && !math.IsNaN(total) && !math.IsInf(total, 0) {
			raw.CPUPercent = max(0, min(100, 100*(total-idle)/total))
		}
		previous := make(map[string]NetworkMetrics, len(old.Networks))
		for _, item := range old.Networks {
			previous[item.Name] = item
		}
		seconds := at.Sub(s.previousAt).Seconds()
		for i := range raw.Networks {
			item := &raw.Networks[i]
			if previous, exists := previous[item.Name]; exists {
				if item.BytesSent >= previous.BytesSent {
					item.SendBytesPerSec = float64(item.BytesSent-previous.BytesSent) / seconds
				}
				if item.BytesReceived >= previous.BytesReceived {
					item.ReceiveBytesPerSec = float64(item.BytesReceived-previous.BytesReceived) / seconds
				}
			}
		}
	}
	slices.SortFunc(raw.Disks, func(a, b DiskMetrics) int {
		if a.Path < b.Path {
			return -1
		}
		if a.Path > b.Path {
			return 1
		}
		return 0
	})
	slices.SortFunc(raw.Networks, func(a, b NetworkMetrics) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	if err := raw.SystemMetrics.Validate(); err != nil {
		return SystemMetrics{}, err
	}
	copy := raw
	copy.Disks, copy.Networks = slices.Clone(raw.Disks), slices.Clone(raw.Networks)
	s.previous, s.previousAt = &copy, at
	return raw.SystemMetrics, nil
}

func (m SystemMetrics) Validate() error {
	percent := func(value float64) bool {
		return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 100
	}
	if !percent(m.CPUPercent) || !percent(m.MemoryPercent) || m.MemoryUsed > m.MemoryTotal || m.MemoryAvailable > m.MemoryTotal || m.MemoryTotal > math.MaxInt64 || m.UptimeSeconds > math.MaxInt64 {
		return fmt.Errorf("系统 CPU/内存百分比或计数无效")
	}
	seen := make(map[string]bool)
	for _, disk := range m.Disks {
		key := strings.ToLower(disk.Path)
		if disk.Path == "" || seen[key] || disk.Total > math.MaxInt64 || disk.Used > disk.Total || disk.Free > disk.Total || !percent(disk.UsedPercent) {
			return fmt.Errorf("磁盘路径、容量或使用率无效")
		}
		seen[key] = true
	}
	clear(seen)
	for _, network := range m.Networks {
		if network.Name == "" || seen[network.Name] || network.BytesSent > math.MaxInt64 || network.BytesReceived > math.MaxInt64 || math.IsNaN(network.SendBytesPerSec) || math.IsInf(network.SendBytesPerSec, 0) || network.SendBytesPerSec < 0 || math.IsNaN(network.ReceiveBytesPerSec) || math.IsInf(network.ReceiveBytesPerSec, 0) || network.ReceiveBytesPerSec < 0 {
			return fmt.Errorf("网络接口、计数或速率无效")
		}
		seen[network.Name] = true
	}
	return nil
}
