package collector

import (
	"slices"
	"sync"
)

type ProcessPort struct {
	Address string `json:"address"`
	Port    uint32 `json:"port"`
}

type ProcessInfo struct {
	PID        int32         `json:"pid"`
	ParentPID  int32         `json:"parentPid"`
	Name       string        `json:"name"`
	Exe        string        `json:"exe"`
	CreatedAt  int64         `json:"createdAt"`
	CPUPercent float64       `json:"cpuPercent"`
	RSS        uint64        `json:"rss"`
	Threads    int32         `json:"threads"`
	Ports      []ProcessPort `json:"ports"`
	Services   []string      `json:"services"`
}

type ProcessCollector struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
}

type ProcessList struct {
	Collector   ProcessCollector `json:"collector"`
	CollectedAt int64            `json:"collectedAt"`
	Processes   []ProcessInfo    `json:"processes"`
	Truncated   bool             `json:"truncated"`
	Reason      *string          `json:"reason" jsonschema:"nullable"`
}

type ProcessSnapshot struct {
	CollectedAt   int64
	Processes     []Process
	Ports         []Port
	Services      []Service
	ServicesKnown bool
}

type ProcessSnapshots interface {
	Latest() ProcessSnapshot
}

// SnapshotCache retains local command lines only for matching, never for serialization.
type SnapshotCache struct {
	mu       sync.RWMutex
	snapshot ProcessSnapshot
}

func (c *SnapshotCache) UpdateProcesses(at int64, processes []Process, ports []Port) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot.CollectedAt = at
	c.snapshot.Processes, c.snapshot.Ports = slices.Clone(processes), slices.Clone(ports)
}

func (c *SnapshotCache) UpdateServices(services []Service) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot.Services, c.snapshot.ServicesKnown = slices.Clone(services), true
}

func (c *SnapshotCache) Latest() ProcessSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := c.snapshot
	result.Processes, result.Ports, result.Services = slices.Clone(result.Processes), slices.Clone(result.Ports), slices.Clone(result.Services)
	return result
}
