// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package cloud

import (
	"math"
	"slices"

	"github.com/LFenX/lg-monitor/internal/api"
)

// Limits keep one device's resource sample to a few kilobytes on the wire. The device page shows no more
// than this anyway: the heaviest programs and containers, the disks and network cards.
const (
	maxSyncedComputers  = 8
	maxSyncedCores      = 256
	maxSyncedDisks      = 16
	maxSyncedNetworks   = 16
	maxSyncedPrograms   = 15
	maxSyncedContainers = 20
)

// CompactDevices trims the device list for the state frame: only computers with a sample, rounded figures,
// and the longest lists cut. Errors and container failures stay on the device that saw them.
func CompactDevices(devices []api.Device) []api.Device {
	result := []api.Device{}
	for _, device := range devices {
		if device.System == nil || len(result) == maxSyncedComputers {
			continue
		}
		system := *device.System
		system.CPUPercent, system.MemoryPercent, system.SwapPercent = round1(system.CPUPercent), round1(system.MemoryPercent), round1(system.SwapPercent)
		system.Load1, system.Load5, system.Load15 = round1(system.Load1), round1(system.Load5), round1(system.Load15)
		system.CorePercent = slices.Clone(system.CorePercent[:min(len(system.CorePercent), maxSyncedCores)])
		for index, value := range system.CorePercent {
			system.CorePercent[index] = math.Round(value)
		}
		system.Disks = slices.Clone(system.Disks[:min(len(system.Disks), maxSyncedDisks)])
		system.Networks = slices.Clone(system.Networks[:min(len(system.Networks), maxSyncedNetworks)])
		system.Errors, system.Top, system.Containers, system.ContainerError = nil, nil, nil, ""
		device.System = &system
		if usage := device.Usage; usage != nil {
			trimmed := *usage
			trimmed.Top = slices.Clone(usage.Top[:min(len(usage.Top), maxSyncedPrograms)])
			for index := range trimmed.Top {
				trimmed.Top[index].CPUPercent = round1(trimmed.Top[index].CPUPercent)
			}
			trimmed.Containers = slices.Clone(usage.Containers[:min(len(usage.Containers), maxSyncedContainers)])
			for index := range trimmed.Containers {
				trimmed.Containers[index].CPUPercent = round1(trimmed.Containers[index].CPUPercent)
			}
			trimmed.ContainerError = ""
			device.Usage = &trimmed
		}
		result = append(result, device)
	}
	return result
}

func round1(value float64) float64 { return math.Round(value*10) / 10 }
