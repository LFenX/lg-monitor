// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"slices"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/cloud"
	"github.com/LFenX/lg-monitor/internal/config"
)

// cloudGuestScope is what a LAN guest may see of the owner's other synced devices. Nothing is shared until the
// owner picks a device's boards: those computers are not on this network and their owners may not expect it.
type cloudGuestScope struct {
	shared  []string
	visible map[string]bool
	names   map[string]string
}

func (b *APIBackend) cloudGuestOperation(ctx context.Context, cfg *config.Config, op api.Operation, r api.Request) (any, error) {
	mgr := b.options.Cloud
	if op == api.ListCloudDevices {
		if mgr == nil {
			return []api.CloudDeviceSummary{}, nil
		}
		return guestCloudDevices(cfg, mgr.Devices()), nil
	}
	if mgr == nil {
		return nil, guestForbidden()
	}
	deviceID := r.Params["deviceId"]
	shared := cfg.Settings.LAN.WebDeviceBoards[deviceID]
	if len(shared) == 0 || isCurrentCloudDevice(mgr.Devices(), deviceID) {
		return nil, guestForbidden()
	}
	switch op {
	case api.GetCloudDeviceBoard:
		if !slices.Contains(shared, r.Params["boardId"]) {
			return nil, guestForbidden()
		}
		view, err := mgr.DeviceBoard(deviceID, r.Params["boardId"])
		if err != nil {
			return nil, missingAPI(err.Error())
		}
		scope := newCloudGuestScope(mgr, deviceID, shared)
		view.Board.Unassigned = []string{}
		view.Board.Graph = nil
		for i, item := range view.Board.Items {
			view.Board.Items[i] = scope.monitor(item)
		}
		return api.CloudBoardView{
			DeviceID:     view.DeviceID,
			DeviceName:   view.DeviceName,
			DeviceAvatar: view.DeviceAvatar,
			Online:       view.Online,
			LastSeen:     view.LastSeen,
			StateUpdated: view.StateUpdated,
			Board:        view.Board,
		}, nil
	case api.GetCloudDeviceMonitor, api.GetCloudDeviceDoc, api.GetCloudDeviceJobDays, api.GetCloudDeviceJobDay:
		scope := newCloudGuestScope(mgr, deviceID, shared)
		if !scope.visible[r.Params["monitorId"]] {
			return nil, guestForbidden()
		}
		result, err := b.cloudOwnerOperation(ctx, op, r)
		if err != nil || op != api.GetCloudDeviceMonitor {
			return result, err
		}
		return scope.detail(result.(api.MonitorDetail)), nil
	}
	return nil, guestForbidden()
}

func isCurrentCloudDevice(devices []cloud.DeviceSummary, id string) bool {
	for _, d := range devices {
		if d.ID == id {
			return d.IsCurrent
		}
	}
	return true
}

func guestCloudDevices(cfg *config.Config, devices []cloud.DeviceSummary) []api.CloudDeviceSummary {
	out := []api.CloudDeviceSummary{}
	for _, d := range devices {
		shared := cfg.Settings.LAN.WebDeviceBoards[d.ID]
		if d.IsCurrent || len(shared) == 0 {
			continue
		}
		boards := slices.DeleteFunc(slices.Clone(d.Boards), func(info api.BoardInfo) bool { return !slices.Contains(shared, info.ID) })
		if len(boards) == 0 {
			continue
		}
		total, down := 0, 0
		for _, info := range boards {
			total += info.Summary.Services + info.Summary.Children
			down += info.Summary.Down
		}
		out = append(out, api.CloudDeviceSummary{
			ID: d.ID, Name: d.Name, Avatar: d.Avatar, OS: d.OS, Online: d.Online, LastSeen: d.LastSeen,
			StateUpdated: d.StateUpdated, TotalCount: total, DownCount: down, Boards: boards,
		})
	}
	return out
}

// cloudBoards is the part of the cloud manager a guest scope reads.
type cloudBoards interface {
	Devices() []cloud.DeviceSummary
	DeviceBoard(deviceID, boardID string) (cloud.SyncedBoardView, error)
}

func newCloudGuestScope(mgr cloudBoards, deviceID string, shared []string) cloudGuestScope {
	scope := cloudGuestScope{shared: shared, visible: map[string]bool{}, names: map[string]string{}}
	for _, d := range mgr.Devices() {
		if d.ID != deviceID {
			continue
		}
		for _, info := range d.Boards {
			view, err := mgr.DeviceBoard(deviceID, info.ID)
			if err != nil {
				continue
			}
			for _, item := range view.Board.Items {
				scope.names[item.ID] = firstOf(item.Name, item.ID)
				if slices.Contains(shared, info.ID) {
					scope.visible[item.ID] = true
				}
			}
		}
	}
	return scope
}

func (s cloudGuestScope) links(edges []api.MonitorDependency) []api.MonitorDependency {
	result := slices.Clone(edges)
	for i, e := range result {
		if !s.visible[e.ID] {
			result[i].RestrictedName = firstOf(s.names[e.ID], e.ID)
			result[i].Note = ""
		}
	}
	return result
}

func (s cloudGuestScope) monitor(m api.Monitor) api.Monitor {
	if !s.visible[m.Parent] {
		m.Parent = ""
	}
	m.Children = slices.DeleteFunc(slices.Clone(m.Children), func(id string) bool { return !s.visible[id] })
	m.Boards = slices.DeleteFunc(slices.Clone(m.Boards), func(id string) bool { return !slices.Contains(s.shared, id) })
	m.Depends = s.links(m.Depends)
	m.Dependents = s.links(m.Dependents)
	return m
}

func (s cloudGuestScope) detail(d api.MonitorDetail) api.MonitorDetail {
	d.Monitor = s.monitor(d.Monitor)
	d.Children = slices.DeleteFunc(d.Children, func(m api.Monitor) bool { return !s.visible[m.ID] })
	for i, child := range d.Children {
		d.Children[i] = s.monitor(child)
	}
	d.Boards = slices.DeleteFunc(d.Boards, func(ref api.BoardRef) bool { return !slices.Contains(s.shared, ref.ID) })
	d.Lineage = slices.DeleteFunc(d.Lineage, func(ref api.MonitorRef) bool { return !s.visible[ref.ID] })
	d.Incidents = slices.DeleteFunc(d.Incidents, func(i api.MonitorIncident) bool { return !s.visible[i.MonitorID] })
	return d
}
