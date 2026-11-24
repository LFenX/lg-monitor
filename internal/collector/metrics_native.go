// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import (
	"context"
	"fmt"
	"runtime"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
)

func (nativeMetrics) Read(ctx context.Context) (rawSystemMetrics, error) {
	var result rawSystemMetrics
	times, err := cpu.TimesWithContext(ctx, false)
	if err != nil {
		return result, fmt.Errorf("读取 CPU 时间失败：%w", err)
	}
	if len(times) != 1 {
		return result, fmt.Errorf("CPU 时间未返回整机汇总")
	}
	result.cpuTotal, result.cpuIdle = times[0].Total(), times[0].Idle+times[0].Iowait
	// Per-core figures and load are extras: a platform that lacks them still reports the rest.
	if cores, err := cpu.TimesWithContext(ctx, true); err == nil && len(cores) > 1 {
		result.CPUCores = len(cores)
		for _, core := range cores {
			result.coreTotal = append(result.coreTotal, core.Total())
			result.coreIdle = append(result.coreIdle, core.Idle+core.Iowait)
		}
	} else if count, err := cpu.CountsWithContext(ctx, true); err == nil {
		result.CPUCores = count
	}
	if runtime.GOOS != "windows" {
		if average, err := load.AvgWithContext(ctx); err == nil {
			result.Load1, result.Load5, result.Load15 = average.Load1, average.Load5, average.Load15
		}
	}
	if swap, err := mem.SwapMemoryWithContext(ctx); err == nil {
		result.SwapTotal, result.SwapUsed, result.SwapPercent = swap.Total, swap.Used, swap.UsedPercent
	}
	memory, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return result, fmt.Errorf("读取物理内存失败：%w", err)
	}
	result.MemoryTotal, result.MemoryUsed, result.MemoryAvailable, result.MemoryPercent = memory.Total, memory.Used, memory.Available, memory.UsedPercent
	result.MemoryCached = memory.Cached + memory.Buffers
	result.UptimeSeconds, err = host.UptimeWithContext(ctx)
	if err != nil {
		return result, fmt.Errorf("读取开机时长失败：%w", err)
	}
	partitions, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return result, fmt.Errorf("读取磁盘分区失败：%w", err)
	}
	seen := make(map[string]bool)
	for _, partition := range partitions {
		if seen[partition.Mountpoint] {
			continue
		}
		seen[partition.Mountpoint] = true
		usage, err := disk.UsageWithContext(ctx, partition.Mountpoint)
		if err != nil {
			result.Errors = append(result.Errors, "磁盘 "+partition.Mountpoint+" 容量读取失败，请检查可用性与权限")
			continue
		}
		result.Disks = append(result.Disks, DiskMetrics{Path: partition.Mountpoint, Total: usage.Total, Used: usage.Used, Free: usage.Free, UsedPercent: usage.UsedPercent})
	}
	networks, err := gnet.IOCountersWithContext(ctx, true)
	if err != nil {
		return result, fmt.Errorf("读取网卡计数失败：%w", err)
	}
	for _, network := range networks {
		result.Networks = append(result.Networks, NetworkMetrics{Name: network.Name, BytesSent: network.BytesSent, BytesReceived: network.BytesRecv})
	}
	return result, nil
}
