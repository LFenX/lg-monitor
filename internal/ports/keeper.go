// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package ports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/LFenX/lg-monitor/internal/durable"
)

// Keeper registers lgmon's ports with Windows as administered exclusions. Such a port stays free for a program
// that asks for it by number, but Windows no longer gives it away in a reserved block. A registration survives
// restarts, so one start on a lucky boot settles the matter for good.
type Keeper struct {
	Path string
	// Run executes netsh; it exists on Windows only and needs administrator rights.
	Run func(ctx context.Context, args ...string) (string, error)
}

// kept is what lgmon registered itself, so uninstalling removes that and nothing else.
type kept struct {
	TCP []int `json:"tcp,omitempty"`
	UDP []int `json:"udp,omitempty"`
}

var rangeLine = regexp.MustCompile(`^\s*(\d+)\s+(\d+)\s*\*?\s*$`)

type span struct{ from, to int }

// parseRanges reads the numbers only: the headings are translated.
func parseRanges(output string) []span {
	var result []span
	for _, line := range strings.Split(output, "\n") {
		if match := rangeLine.FindStringSubmatch(line); match != nil {
			from, _ := strconv.Atoi(match[1])
			to, _ := strconv.Atoi(match[2])
			result = append(result, span{from, to})
		}
	}
	return result
}

func (k Keeper) load() kept {
	var value kept
	if data, err := os.ReadFile(k.Path); err == nil {
		_ = json.Unmarshal(data, &value)
	}
	return value
}

func (k Keeper) save(value kept) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return durable.WriteFile(k.Path, data)
}

// Keep registers the ports that are not registered yet. It must run before lgmon binds them. Ports Windows has
// already given away this boot are refused and tried again on the next start.
func (k Keeper) Keep(ctx context.Context, tcp, udp []int) error {
	record := k.load()
	changed := false
	ranges := func(protocol string) ([]span, error) {
		output, err := k.Run(ctx, "int", "ipv4", "show", "excludedportrange", "protocol="+protocol, "store=persistent")
		if err != nil {
			return nil, fmt.Errorf("读取 Windows 端口登记失败：%w %s", err, output)
		}
		return parseRanges(output), nil
	}
	covered := func(list []span, port int) bool {
		return slices.ContainsFunc(list, func(s span) bool { return s.from <= port && port <= s.to })
	}
	keep := func(protocol string, wanted []int, mine *[]int) error {
		list, err := ranges(protocol)
		if err != nil {
			return err
		}
		var failures []error
		var added []int
		for _, port := range wanted {
			if port < 1024 || port > 65535 || covered(list, port) || slices.Contains(added, port) {
				continue
			}
			if output, err := k.Run(ctx, addArgs(protocol, port)...); err != nil {
				failures = append(failures, fmt.Errorf("%s %d：%w %s", protocol, port, err, output))
				continue
			}
			added = append(added, port)
		}
		if len(added) > 0 {
			// netsh reports success for some commands it did not carry out.
			if list, err = ranges(protocol); err != nil {
				return errors.Join(append(failures, err)...)
			}
		}
		for _, port := range added {
			if !covered(list, port) {
				failures = append(failures, fmt.Errorf("%s %d：Windows 没有登记这个端口", protocol, port))
			} else if !slices.Contains(*mine, port) {
				*mine = append(*mine, port)
				changed = true
			}
		}
		return errors.Join(failures...)
	}
	err := errors.Join(keep("tcp", tcp, &record.TCP), keep("udp", udp, &record.UDP))
	if changed {
		err = errors.Join(err, k.save(record))
	}
	return err
}

// Release removes what Keep registered.
func (k Keeper) Release(ctx context.Context) error {
	record := k.load()
	if len(record.TCP)+len(record.UDP) == 0 {
		return nil
	}
	var failures []error
	release := func(protocol string, mine []int) []int {
		var left []int
		for _, port := range mine {
			if output, err := k.Run(ctx, rangeArgs("delete", protocol, port)...); err != nil {
				failures = append(failures, fmt.Errorf("%s %d：%w %s", protocol, port, err, output))
				left = append(left, port)
			}
		}
		return left
	}
	left := kept{TCP: release("tcp", record.TCP), UDP: release("udp", record.UDP)}
	return errors.Join(append(failures, k.save(left))...)
}

// KeeperFor is the keeper of a data directory.
func KeeperFor(home string) Keeper {
	return Keeper{Path: filepath.Join(home, "data", "kept-ports.json"), Run: netsh}
}
