// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"slices"
	"strings"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/cloud"
)

// crossDeviceRelations draws the account's dependencies across devices from where each one is written: this
// computer's services from its own configuration, the other devices' from what they last synced.
func (b *APIBackend) crossDeviceRelations(ctx context.Context) (api.CrossDeviceRelations, error) {
	result := api.CrossDeviceRelations{Relations: []api.CrossDeviceRelation{}}
	mgr := b.options.Cloud
	if mgr == nil || !mgr.SignedIn() {
		return result, nil
	}
	cfg := b.options.Config.Snapshot()
	local, err := b.monitors(ctx, cfg)
	if err != nil {
		return result, err
	}
	self := mgr.CurrentDevice()
	name, avatar := b.CloudIdentity(ctx)
	if status := mgr.Status(ctx); status.DeviceName != "" {
		name = status.DeviceName
	}
	byID := make(map[string]api.Monitor, len(local))
	for _, monitor := range local {
		byID[monitor.ID] = monitor
	}
	here := func(id string) api.RelationEnd {
		end := api.RelationEnd{Device: self, DeviceName: firstOf(name, "本机"), DeviceAvatar: avatar, Current: true, Online: true, ID: id, Name: id, State: "unknown"}
		if monitor, ok := byID[id]; ok {
			end.Name, end.State, end.Issue = firstOf(monitor.Name, id), firstOf(monitor.State, "unknown"), monitor.Issue
		} else {
			end.Issue = "这台电脑上已经没有这个服务"
		}
		return end
	}
	there := func(device, id string) api.RelationEnd {
		if device == self {
			return here(id)
		}
		service := b.externalService(api.MonitorDependency{Device: device, ID: id}, "")
		return api.RelationEnd{Device: device, DeviceName: service.DeviceName, DeviceAvatar: service.DeviceAvatar, Online: service.Online, ID: id, Name: service.Name, State: service.State, Issue: service.Issue}
	}
	seen := map[string]bool{}
	add := func(from api.RelationEnd, link api.MonitorDependency) {
		key := from.Device + "/" + from.ID + ">" + link.Device + "/" + link.ID
		if seen[key] || link.Device == from.Device {
			return
		}
		seen[key] = true
		result.Relations = append(result.Relations, api.CrossDeviceRelation{From: from, To: there(link.Device, link.ID), Relation: link.Relation})
	}
	for _, monitor := range local {
		for _, link := range monitor.External {
			add(here(monitor.ID), link)
		}
	}
	for _, item := range mgr.ExternalLinks() {
		from := syncedEnd(item)
		for _, link := range item.Monitor.External {
			add(from, link)
		}
	}
	devices := map[string]bool{}
	for _, relation := range result.Relations {
		devices[relation.From.Device], devices[relation.To.Device] = true, true
		if troubled(relation.From) || troubled(relation.To) {
			result.Problems++
		}
	}
	result.Devices = len(devices)
	// Troubled relations first, so a small overview shows what needs a look.
	slices.SortStableFunc(result.Relations, func(a, c api.CrossDeviceRelation) int {
		at, ct := troubled(a.From) || troubled(a.To), troubled(c.From) || troubled(c.To)
		if at != ct {
			if at {
				return -1
			}
			return 1
		}
		return strings.Compare(a.From.DeviceName+a.From.Name+a.To.Name, c.From.DeviceName+c.From.Name+c.To.Name)
	})
	return result, nil
}

func syncedEnd(item cloud.SyncedLink) api.RelationEnd {
	end := api.RelationEnd{Device: item.Device.ID, DeviceName: item.Device.Name, DeviceAvatar: item.Device.Avatar, Online: item.Device.Online, ID: item.Monitor.ID, Name: firstOf(item.Monitor.Name, item.Monitor.ID), State: firstOf(item.Monitor.State, "unknown"), Issue: item.Monitor.Issue}
	if !end.Online {
		end.State, end.Issue = "unknown", "设备离线，状态可能过时"
	}
	return end
}

// troubled is an end that is down, or whose state cannot be trusted right now.
func troubled(end api.RelationEnd) bool {
	return end.State != "up" || !end.Online
}
