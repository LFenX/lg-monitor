// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/osinfo"
	"github.com/LFenX/lg-monitor/internal/store"
)

type deviceSnapshots interface {
	DeviceSnapshot(context.Context, string) (store.DeviceSnapshot, error)
}

// devicePreset is one of the warnings the device page can switch on; each is a metric_threshold rule without a
// target, so the rules engine checks it on every computer separately.
type devicePreset struct {
	key, metric, name, severity string
	defaults                    api.DeviceAlert
	pick                        func(*api.DeviceAlerts) *api.DeviceAlert
}

var devicePresets = []devicePreset{
	{"memory", "system.memory_percent", "内存即将耗尽（OOM 预警）", "critical", api.DeviceAlert{Threshold: 92, For: "2m"}, func(a *api.DeviceAlerts) *api.DeviceAlert { return &a.Memory }},
	{"swap", "system.swap_percent", "交换区快用完了", "warning", api.DeviceAlert{Threshold: 80, For: "5m"}, func(a *api.DeviceAlerts) *api.DeviceAlert { return &a.Swap }},
	{"cpu", "system.cpu_percent", "CPU 持续繁忙", "warning", api.DeviceAlert{Threshold: 90, For: "10m"}, func(a *api.DeviceAlerts) *api.DeviceAlert { return &a.CPU }},
	{"disk", "disk.used_percent", "磁盘快满了", "warning", api.DeviceAlert{Threshold: 90, For: "5m"}, func(a *api.DeviceAlerts) *api.DeviceAlert { return &a.Disk }},
	{"container-memory", "container.memory_percent", "容器内存接近上限（OOM 预警）", "critical", api.DeviceAlert{Threshold: 90, For: "2m"}, func(a *api.DeviceAlerts) *api.DeviceAlert { return &a.ContainerMemory }},
}

func (b *APIBackend) deviceOperation(ctx context.Context, cfg *config.Config, op api.Operation, r api.Request) (any, error) {
	switch op {
	case api.ListDevices:
		return b.devices(ctx)
	case api.GetDeviceAlerts:
		return deviceAlerts(cfg), nil
	case api.PutDeviceAlerts:
		var input api.DeviceAlerts
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		return b.putDeviceAlerts(ctx, input)
	case api.PutProgramName:
		var input api.ProgramNameInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		return b.putProgramName(ctx, input)
	}
	return nil, missingAPI("未知的设备操作")
}

func (b *APIBackend) devices(ctx context.Context) ([]api.Device, error) {
	presences, err := b.options.Collectors.Collectors(ctx)
	if err != nil {
		return nil, err
	}
	source, _ := b.options.Read.(deviceSnapshots)
	cfg := b.options.Config.Snapshot()
	localHost, _ := os.Hostname()
	result := []api.Device{}
	for _, item := range presences {
		device := api.Device{ID: item.ID, Hostname: item.Hostname, LastSeen: item.LastSeen, Online: item.Connected && b.options.Now().UnixMilli()-item.LastSeen <= 30000}
		if item.ID == "local" || (localHost != "" && strings.EqualFold(item.Hostname, localHost)) {
			device.OS = osinfo.Label()
		}
		if metadata, _, err := b.options.Read.Collector(ctx, item.ID); err == nil {
			device.Version = metadata.Version
		}
		if source != nil {
			snapshot, err := source.DeviceSnapshot(ctx, item.ID)
			if err != nil {
				return nil, err
			}
			if latest := snapshot.Latest; latest != nil {
				// The latest sample's own usage lists, if any, are shown from Usage, not twice.
				system := *latest
				system.Top, system.Containers, system.ContainerError = nil, nil, ""
				system.Disks, system.Networks = nonNil(system.Disks), nonNil(system.Networks)
				device.System = &system
			}
			if usage := snapshot.Usage; usage != nil {
				device.Usage = &api.DeviceUsage{TS: usage.TS, ProcessCount: usage.ProcessCount, Top: nameProgram(cfg, slices.Clone(nonNil(usage.Top))), Containers: nonNil(usage.Containers), ContainerError: usage.ContainerError}
			}
		}
		result = append(result, device)
	}
	return result, nil
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

func deviceRuleID(key string) string { return api.DeviceRulePrefix + key }

func deviceAlerts(cfg *config.Config) api.DeviceAlerts {
	var result api.DeviceAlerts
	for _, preset := range devicePresets {
		item := preset.pick(&result)
		*item = preset.defaults
		index := slices.IndexFunc(cfg.Rules, func(rule config.Rule) bool { return rule.ID == deviceRuleID(preset.key) })
		if index < 0 {
			continue
		}
		rule := cfg.Rules[index]
		item.Enabled = true
		if rule.Threshold != nil {
			item.Threshold = *rule.Threshold
		}
		if rule.For != "" {
			item.For = string(rule.For)
		}
		if len(result.Notify) == 0 {
			result.Notify = slices.Clone(rule.Notify)
		}
	}
	if len(result.Notify) == 0 {
		for _, channel := range cfg.Notifiers {
			result.Notify = append(result.Notify, channel.ID)
		}
	}
	if result.Notify == nil {
		result.Notify = []string{}
	}
	return result
}

func (b *APIBackend) putDeviceAlerts(ctx context.Context, input api.DeviceAlerts) (api.DeviceAlerts, error) {
	cfg := b.options.Config.Snapshot()
	var wanted []config.Rule
	notify := []string{}
	for _, channel := range cfg.Notifiers {
		notify = append(notify, channel.ID)
	}
	created := ""
	for _, preset := range devicePresets {
		item := *preset.pick(&input)
		if !item.Enabled {
			continue
		}
		if item.Threshold < 1 || item.Threshold > 100 {
			return api.DeviceAlerts{}, invalidAPI(preset.name + "的阈值要在 1～100 之间")
		}
		hold, err := time.ParseDuration(item.For)
		if item.For == "" {
			hold, err = 0, nil
		}
		if err != nil || hold < 0 || hold > 24*time.Hour {
			return api.DeviceAlerts{}, invalidAPI(preset.name + "的持续时间要写成 30s、2m、1h 这样，最长 24h")
		}
		if len(notify) == 0 {
			// A warning nobody hears is worse than a local toast, which needs no credentials.
			created, notify = "desktop", []string{"desktop"}
		}
		threshold := item.Threshold
		rule := config.Rule{ID: deviceRuleID(preset.key), Name: preset.name, Type: "metric_threshold", Metric: preset.metric, Op: "gte", Threshold: &threshold, Severity: preset.severity, Notify: slices.Clone(notify), Renotify: "1h"}
		if hold > 0 {
			rule.For = config.Duration(item.For)
		}
		wanted = append(wanted, rule)
	}
	if created != "" {
		if _, err := b.options.Editor.Update(ctx, "notifiers.yaml", func(old []byte) ([]byte, error) {
			file, err := config.ParseNotifiers(old)
			if err != nil {
				return nil, err
			}
			if !slices.ContainsFunc(file.Notifiers, func(channel config.Notifier) bool { return channel.ID == created }) {
				file.Notifiers = append(file.Notifiers, config.Notifier{ID: created, Type: "toast"})
			}
			return encodeYAML(file)
		}); err != nil {
			return api.DeviceAlerts{}, err
		}
	}
	presetIDs := map[string]bool{}
	for _, preset := range devicePresets {
		presetIDs[deviceRuleID(preset.key)] = true
	}
	if err := b.editRules(ctx, func(items []config.Rule) []config.Rule {
		items = slices.DeleteFunc(items, func(rule config.Rule) bool { return presetIDs[rule.ID] })
		return append(items, wanted...)
	}); err != nil {
		return api.DeviceAlerts{}, err
	}
	return deviceAlerts(b.options.Config.Snapshot()), nil
}

// CloudDevices lets cloud sync carry the computers' latest samples to the account's other devices; the
// warnings stay on this device.
func (b *APIBackend) CloudDevices(ctx context.Context) ([]api.Device, error) {
	devices, err := b.devices(ctx)
	if err != nil {
		return nil, err
	}
	histories := b.deviceHistories(ctx, devices)
	for index := range devices {
		devices[index].History = histories[devices[index].ID]
	}
	return devices, nil
}
