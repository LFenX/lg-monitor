// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// MaxTopProcesses bounds the per-sample process list: the heaviest by memory, then by CPU.
	MaxTopProcesses = 20
	// MaxContainers bounds the container list one sample may carry.
	MaxContainers = 100
)

// ProcessUsage is what one program uses, its processes added together, so forty browser processes read as one line.
type ProcessUsage struct {
	Name       string  `json:"name"`
	Count      int     `json:"count"`
	PID        int32   `json:"pid,omitempty"`
	CPUPercent float64 `json:"cpuPercent"`
	RSS        uint64  `json:"rss"`
}

// ContainerUsage is one running container as the container engine reports it.
type ContainerUsage struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	CPUPercent    float64 `json:"cpuPercent"`
	MemoryUsed    uint64  `json:"memoryUsed"`
	MemoryLimit   uint64  `json:"memoryLimit"`
	MemoryPercent float64 `json:"memoryPercent"`
	PIDs          int     `json:"pids"`
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

// TopProcesses groups processes by program name and keeps the heaviest: most memory first, then the busiest CPU
// users not already listed.
func TopProcesses(processes []Process) []ProcessUsage {
	groups := map[string]*ProcessUsage{}
	for _, process := range processes {
		name := strings.TrimSpace(process.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		group := groups[key]
		if group == nil {
			group = &ProcessUsage{Name: truncate(name, 80), PID: process.PID}
			groups[key] = group
		}
		group.Count++
		if group.Count > 1 {
			group.PID = 0
		}
		group.RSS += process.RSS
		if finite(process.CPUPercent) && process.CPUPercent > 0 {
			group.CPUPercent = min(100, group.CPUPercent+process.CPUPercent)
		}
	}
	all := make([]ProcessUsage, 0, len(groups))
	for _, group := range groups {
		all = append(all, *group)
	}
	slices.SortFunc(all, func(a, b ProcessUsage) int {
		return cmp.Or(cmp.Compare(b.RSS, a.RSS), strings.Compare(a.Name, b.Name))
	})
	byMemory := MaxTopProcesses * 3 / 4
	result := slices.Clone(all[:min(len(all), byMemory)])
	rest := slices.Clone(all[min(len(all), byMemory):])
	slices.SortFunc(rest, func(a, b ProcessUsage) int {
		return cmp.Or(cmp.Compare(b.CPUPercent, a.CPUPercent), strings.Compare(a.Name, b.Name))
	})
	for _, item := range rest {
		if len(result) >= MaxTopProcesses || item.CPUPercent < 0.5 {
			break
		}
		result = append(result, item)
	}
	return result
}

func truncate(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

// ContainerSource reads the containers running on this computer.
type ContainerSource interface {
	Containers(context.Context) ([]ContainerUsage, error)
}

// ErrNoContainerEngine means there is no container engine to ask; it is not a failure worth showing.
var ErrNoContainerEngine = errors.New("没有找到 docker 命令")

// DockerCLI asks the docker command for one round of stats. The command is looked up again every ten minutes,
// so installing Docker later is noticed without a restart.
type DockerCLI struct {
	Now    func() time.Time
	mu     sync.Mutex
	path   string
	looked time.Time
	run    func(ctx context.Context, path string) ([]byte, error)
}

func (d *DockerCLI) command() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	if d.looked.IsZero() || now().Sub(d.looked) > 10*time.Minute {
		d.looked = now()
		d.path, _ = exec.LookPath("docker")
	}
	return d.path
}

func (d *DockerCLI) Containers(ctx context.Context) ([]ContainerUsage, error) {
	path := d.command()
	if path == "" {
		return nil, ErrNoContainerEngine
	}
	run := d.run
	if run == nil {
		run = runDockerStats
	}
	out, err := run(ctx, path)
	if err != nil {
		return nil, err
	}
	return ParseDockerStats(out)
}

func runDockerStats(ctx context.Context, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "stats", "--no-stream", "--no-trunc", "--format", "{{json .}}")
	hideWindow(command)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if len(message) > 200 {
			message = message[:200]
		}
		if message == "" {
			return nil, fmt.Errorf("docker stats 失败：%w", err)
		}
		return nil, fmt.Errorf("docker stats 失败：%s：%w", message, err)
	}
	return out, nil
}

// ParseDockerStats reads `docker stats --format "{{json .}}"`, one JSON object per line.
func ParseDockerStats(out []byte) ([]ContainerUsage, error) {
	result := []ContainerUsage{}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var row struct {
			ID, Name, CPUPerc, MemUsage, MemPerc, PIDs string
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, fmt.Errorf("docker stats 输出格式无法识别：%w", err)
		}
		item := ContainerUsage{ID: truncate(row.ID, 12), Name: truncate(strings.TrimPrefix(row.Name, "/"), 80)}
		if item.Name == "" {
			item.Name = item.ID
		}
		if item.Name == "" {
			continue
		}
		item.CPUPercent = percentValue(row.CPUPerc)
		item.MemoryPercent = min(100, percentValue(row.MemPerc))
		if used, limit, ok := strings.Cut(row.MemUsage, "/"); ok {
			item.MemoryUsed, item.MemoryLimit = parseSize(used), parseSize(limit)
		}
		item.PIDs, _ = strconv.Atoi(strings.TrimSpace(row.PIDs))
		result = append(result, item)
		if len(result) >= MaxContainers {
			break
		}
	}
	slices.SortFunc(result, func(a, b ContainerUsage) int {
		return cmp.Or(cmp.Compare(b.MemoryUsed, a.MemoryUsed), strings.Compare(a.Name, b.Name))
	})
	return result, scanner.Err()
}

func percentValue(text string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(text), "%"), 64)
	if err != nil || !finite(value) || value < 0 {
		return 0
	}
	return value
}

// parseSize reads docker's sizes, binary (KiB, MiB) and decimal (kB, MB) alike.
func parseSize(text string) uint64 {
	text = strings.TrimSpace(text)
	end := len(text)
	for end > 0 && (text[end-1] < '0' || text[end-1] > '9') {
		end--
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(text[:end]), 64)
	if err != nil || !finite(value) || value < 0 {
		return 0
	}
	units := map[string]float64{"": 1, "b": 1, "kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40, "kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12}
	scale, ok := units[strings.ToLower(strings.TrimSpace(text[end:]))]
	if !ok {
		return 0
	}
	size := value * scale
	if size > math.MaxInt64 {
		return 0
	}
	return uint64(size)
}
