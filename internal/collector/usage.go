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
	Name string `json:"name"`
	// Detail tells apart programs run by the same interpreter: the script or module node, python and the like
	// were started with. Only that short name travels, never the rest of the command line.
	Detail string `json:"detail,omitempty"`
	// DisplayName is the name the owner gave this program, or the monitored service it belongs to; set by the
	// monitor for display only.
	DisplayName string  `json:"displayName,omitempty"`
	Count       int     `json:"count"`
	PID         int32   `json:"pid,omitempty"`
	CPUPercent  float64 `json:"cpuPercent"`
	RSS         uint64  `json:"rss"`
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
		name := programName(process)
		if name == "" {
			continue
		}
		detail := programDetail(name, process.Cmdline)
		key := strings.ToLower(name) + "\x00" + strings.ToLower(detail)
		group := groups[key]
		if group == nil {
			group = &ProcessUsage{Name: truncate(name, 80), Detail: truncate(detail, 80), PID: process.PID}
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
		return cmp.Or(cmp.Compare(b.RSS, a.RSS), strings.Compare(a.Name, b.Name), strings.Compare(a.Detail, b.Detail))
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

// linuxCommLength is where Linux cuts a process name (TASK_COMM_LEN 16, less the terminating NUL).
const linuxCommLength = 15

// programName is the process name, made whole again when Linux cut it to 15 characters ("next-server (v").
func programName(process Process) string {
	name := strings.TrimSpace(process.Name)
	if utf8.RuneCountInString(process.Name) != linuxCommLength {
		return name
	}
	for _, candidate := range []string{baseName(process.Exe), baseName(firstField(process.Cmdline))} {
		if len(candidate) > len(process.Name) && strings.HasPrefix(candidate, process.Name) {
			return candidate
		}
	}
	return name
}

// interpreters run someone else's program; the first argument that is not an option names that program.
var interpreters = map[string]bool{
	"node": true, "nodejs": true, "deno": true, "bun": true, "tsx": true, "ts-node": true,
	"python": true, "python2": true, "python3": true, "pypy": true, "pypy3": true, "uv": true,
	"ruby": true, "php": true, "perl": true, "java": true, "dotnet": true, "pwsh": true, "powershell": true,
	"bash": true, "sh": true, "dash": true, "zsh": true,
}

// programDetail is the script or module an interpreter was started with: "35-sync-mailbox-read-model.ts",
// "app.serve", "server.py". It is empty for any other program.
func programDetail(name, cmdline string) string {
	key := strings.ToLower(strings.TrimSuffix(strings.ToLower(name), ".exe"))
	if !interpreters[key] && !strings.HasPrefix(key, "python3.") {
		return ""
	}
	fields := strings.Fields(cmdline)
	for index := 1; index < len(fields); index++ {
		field := strings.Trim(fields[index], "\"'")
		switch {
		case field == "-m" || field == "-jar":
			if index+1 < len(fields) {
				return baseName(strings.Trim(fields[index+1], "\"'"))
			}
			return ""
		case field == "-c" || field == "-e" || field == "--eval":
			return ""
		case strings.HasPrefix(field, "-"):
			continue
		case interpreters[strings.ToLower(strings.TrimSuffix(baseName(field), ".exe"))]:
			// "node tsx script.ts" and "uv run app.py": the program comes after the second launcher.
			continue
		case field == "run" || field == "exec", launcherScript(field):
			continue
		}
		return baseName(field)
	}
	return ""
}

// launcherScript is a launcher's own entry file, such as ".../tsx/dist/cli.mjs" or ".../ts-node/dist/bin.js":
// the program it starts comes after it.
func launcherScript(path string) bool {
	parts := strings.FieldsFunc(strings.ToLower(path), func(r rune) bool { return r == '/' || r == '\\' })
	for _, part := range parts[:max(len(parts)-1, 0)] {
		if interpreters[part] || part == ".bin" {
			return true
		}
	}
	return false
}

func firstField(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], "\"'")
}

func baseName(path string) string {
	path = strings.TrimRight(path, "/\\")
	if index := strings.LastIndexAny(path, "/\\"); index >= 0 {
		path = path[index+1:]
	}
	return path
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
