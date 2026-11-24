// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

import "github.com/LFenX/lg-monitor/internal/collector"

// Device monitoring is about the computers themselves: CPU, memory, swap, disks, and what each program and container
// uses. It is numbers over time rather than up or down, and sits apart from the service boards.
const (
	ListDevices      Operation = "list_devices"
	GetDeviceAlerts  Operation = "get_device_alerts"
	PutDeviceAlerts  Operation = "put_device_alerts"
	DeviceRulePrefix           = "device-"
)

// Device is one computer with a collector: its latest sample and, from the last half minute or so, who uses what.
type Device struct {
	ID       string                   `json:"id" jsonschema_description:"采集器 ID。"`
	Hostname string                   `json:"hostname"`
	OS       string                   `json:"os,omitempty" jsonschema_description:"操作系统与发行版版本，例如 Windows 11 24H2、Debian 12、Ubuntu 24.04。"`
	Version  string                   `json:"version,omitempty"`
	Online   bool                     `json:"online"`
	LastSeen int64                    `json:"lastSeen"`
	System   *collector.SystemMetrics `json:"system" jsonschema:"nullable" jsonschema_description:"最近一次系统采样：CPU、每个核心、负载、内存、交换区、磁盘、网卡。还没有采样时为 null。"`
	Usage    *DeviceUsage             `json:"usage,omitempty" jsonschema_description:"最近一次进程和容器占用（约 30 秒一次）。"`
	// History only travels with cloud sync: this computer's own page reads its curves from GET /api/v1/metrics.
	History *DeviceHistory `json:"history,omitempty" jsonschema_description:"云同步来的 CPU、内存、交换区历史（1h/6h/24h/7d），只出现在同账号其他设备的设备数据里。"`
}

// DeviceHistory is a computer's recent CPU, memory and swap curves at the device page's four ranges.
type DeviceHistory struct {
	Series []DeviceSeries `json:"series"`
}

// DeviceSeries holds one range as evenly spaced buckets from From, Step milliseconds apart; -1 means no data.
type DeviceSeries struct {
	Span   string    `json:"span" jsonschema:"enum=1h,enum=6h,enum=24h,enum=7d"`
	From   int64     `json:"from"`
	Step   int64     `json:"step"`
	CPU    []float64 `json:"cpu"`
	Memory []float64 `json:"mem"`
	Swap   []float64 `json:"swap,omitempty"`
}

type DeviceUsage struct {
	TS             int64                      `json:"ts"`
	ProcessCount   int                        `json:"processCount"`
	Top            []collector.ProcessUsage   `json:"top" jsonschema_description:"按程序名合并的进程：内存最多的在前，其后是 CPU 最忙但不在前面的。"`
	Containers     []collector.ContainerUsage `json:"containers" jsonschema_description:"docker 报告的运行中容器；没有 docker 时为空。"`
	ContainerError string                     `json:"containerError,omitempty" jsonschema_description:"有 docker 但读取失败时的原因。"`
}

// DeviceAlert is one preset warning about the computers: on or off, its threshold and how long it must hold.
type DeviceAlert struct {
	Enabled   bool    `json:"enabled,omitempty"`
	Threshold float64 `json:"threshold,omitempty" jsonschema:"minimum=1,maximum=100" jsonschema_description:"百分比阈值，达到或超过时算满足。"`
	For       string  `json:"for,omitempty" jsonschema_description:"持续多久才告警，例如 2m、10m。"`
}

// DeviceAlerts are the preset device warnings, each one metric_threshold rule with the id device-<kind>.
type DeviceAlerts struct {
	Memory          DeviceAlert `json:"memory,omitempty" jsonschema_description:"内存即将耗尽（OOM 预警）：内存使用率。"`
	Swap            DeviceAlert `json:"swap,omitempty" jsonschema_description:"交换区（Windows 页面文件）使用率。"`
	CPU             DeviceAlert `json:"cpu,omitempty" jsonschema_description:"CPU 持续繁忙。"`
	Disk            DeviceAlert `json:"disk,omitempty" jsonschema_description:"任意一块磁盘使用率。"`
	ContainerMemory DeviceAlert `json:"containerMemory,omitempty" jsonschema_description:"任意一个容器的内存接近它的上限（OOM 预警）。"`
	Notify          []string    `json:"notify,omitempty" jsonschema_description:"发到哪些通知渠道：现有的全部渠道；一个都没有时自动启用本机 Windows 通知。只读。"`
}

func deviceRoutes() []Route {
	return []Route{
		{ListDevices, "GET", "/api/v1/devices", "全部电脑的设备监控：CPU、核心、负载、内存、交换区、磁盘、网卡，以及各程序和容器的占用", ScopeRead, nil, []Device{}, nil},
		{GetDeviceAlerts, "GET", "/api/v1/devices/alerts", "读取设备预警（内存即将耗尽、交换区、CPU、磁盘、容器内存）", ScopeRead, nil, DeviceAlerts{}, nil},
		{PutDeviceAlerts, "PUT", "/api/v1/devices/alerts", "开关和调整设备预警；只改 device- 开头的规则，其他告警不变", ScopeConfig, DeviceAlerts{}, DeviceAlerts{}, nil},
	}
}
