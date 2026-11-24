// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"slices"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
)

// External dependencies are services on another computer of the same cloud account. They are read from what that
// computer last synced and shown apart: this computer's alerts and board state only ever come from its own checks.

// resolveExternal checks the cross-device entries of a dependency list before it is saved. A device ID that turns
// out to be this computer becomes an ordinary local dependency.
func (b *APIBackend) resolveExternal(depends *[]config.Dependency) error {
	if depends == nil {
		return nil
	}
	mgr := b.options.Cloud
	for i := range *depends {
		item := &(*depends)[i]
		if item.Device == "" {
			continue
		}
		if mgr == nil {
			return invalidAPI("当前运行模式没有云同步，不能写其他设备上的服务")
		}
		if item.Device == mgr.CurrentDevice() {
			item.Device = ""
			continue
		}
		if !mgr.SignedIn() {
			return invalidAPI("要先在「设置 → 账号与同步」登录云账号，才能依赖其他设备上的服务")
		}
		device, _, deviceFound, monitorFound := mgr.Lookup(item.Device, item.Target)
		switch {
		case !deviceFound:
			return invalidAPI("同一账号下没有设备 " + item.Device + "；先调用 list_external_services 核对 device")
		case !monitorFound:
			return invalidAPI("设备「" + device.Name + "」上没有服务 " + item.Target + "；先调用 list_external_services 核对 target")
		}
	}
	return nil
}

// namedExternal adds each device's current name to the external links of one service.
func (b *APIBackend) namedExternal(links []api.MonitorDependency) []api.MonitorDependency {
	if len(links) == 0 || b.options.Cloud == nil {
		return links
	}
	for i := range links {
		if device, _, found, _ := b.options.Cloud.Lookup(links[i].Device, links[i].ID); found {
			links[i].DeviceName = device.Name
		}
	}
	return links
}

// boardExternal collects the services on other computers that the board's services use, each once, with who uses it.
func (b *APIBackend) boardExternal(items []api.Monitor) []api.ExternalService {
	var result []api.ExternalService
	index := map[string]int{}
	for _, item := range items {
		for _, link := range item.External {
			key := link.Device + "/" + link.ID
			if at, ok := index[key]; ok {
				if !slices.Contains(result[at].Users, item.ID) {
					result[at].Users = append(result[at].Users, item.ID)
				}
				continue
			}
			index[key] = len(result)
			result = append(result, b.externalService(link, item.ID))
		}
	}
	return result
}

func (b *APIBackend) externalService(link api.MonitorDependency, user string) api.ExternalService {
	service := api.ExternalService{Device: link.Device, DeviceName: firstOf(link.DeviceName, link.Device), ID: link.ID, Name: link.ID, State: "unknown", Users: []string{user}}
	mgr := b.options.Cloud
	if mgr == nil || !mgr.SignedIn() {
		service.Issue = "没有登录云账号，读不到其他设备的状态"
		return service
	}
	device, monitor, deviceFound, monitorFound := mgr.Lookup(link.Device, link.ID)
	if !deviceFound {
		service.Issue = "这台设备已经不在当前账号下"
		return service
	}
	service.DeviceName, service.DeviceAvatar, service.Online = device.Name, device.Avatar, device.Online
	if !monitorFound {
		service.Issue = "那台设备上已经没有这个服务"
		return service
	}
	service.Name, service.Role, service.Parent, service.Uptime = monitor.Name, monitor.Role, monitor.Parent, monitor.Uptime
	if !device.Online {
		// The last synced state may be hours old; it is shown as a hint, never as a live fault.
		service.Issue = "设备离线，状态可能过时"
		return service
	}
	service.State, service.Issue = monitor.State, monitor.Issue
	if service.State == "" {
		service.State = "unknown"
	}
	return service
}

// withExternal counts a board's external dependencies into its summary without touching its own state.
func withExternal(info *api.BoardInfo, external []api.ExternalService) {
	info.Summary.External, info.Summary.ExternalDown = len(external), 0
	for _, service := range external {
		if service.State == "down" {
			info.Summary.ExternalDown++
		}
	}
}
