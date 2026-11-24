// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
)

// A board of another device of the account, opened to the LAN, is offered to members under this ID. "~" keeps
// it apart from local board IDs and needs no escaping in the member port's paths.
const lanDeviceBoardPrefix = "cloud~"

func lanDeviceBoardID(device, board string) string {
	return lanDeviceBoardPrefix + device + "~" + board
}

func splitLANDeviceBoard(id string) (device, board string, ok bool) {
	rest, found := strings.CutPrefix(id, lanDeviceBoardPrefix)
	if !found {
		return "", "", false
	}
	device, board, ok = strings.Cut(rest, "~")
	return device, board, ok && device != "" && board != ""
}

// LANBoardList and LANBoardView let the member port show boards without the rest of the API: this computer's
// boards, then the boards of the account's other devices the owner opened to the LAN.
func (b *APIBackend) LANBoardList(ctx context.Context) ([]api.BoardInfo, error) {
	cfg := b.options.Config.Snapshot()
	list, err := b.boardList(ctx, cfg)
	if err != nil {
		return nil, err
	}
	mgr := b.options.Cloud
	if mgr == nil {
		return list, nil
	}
	for _, device := range mgr.Devices() {
		shared := cfg.Settings.LAN.WebDeviceBoards[device.ID]
		if device.IsCurrent || len(shared) == 0 {
			continue
		}
		for _, info := range device.Boards {
			if !slices.Contains(shared, info.ID) {
				continue
			}
			view, err := b.lanDeviceBoard(cfg, device.ID, info.ID)
			if err != nil {
				continue
			}
			list = append(list, view.BoardInfo)
		}
	}
	return list, nil
}

func (b *APIBackend) lanDeviceBoard(cfg *config.Config, device, board string) (api.BoardView, error) {
	mgr := b.options.Cloud
	shared := cfg.Settings.LAN.WebDeviceBoards[device]
	if mgr == nil || !slices.Contains(shared, board) || isCurrentCloudDevice(mgr.Devices(), device) {
		return api.BoardView{}, missingAPI("这块面板没有开放到内网")
	}
	synced, err := mgr.DeviceBoard(device, board)
	if err != nil {
		return api.BoardView{}, missingAPI(err.Error())
	}
	view := newCloudGuestScope(mgr, device, shared, cfg.Settings.LAN.WebDeviceServices[device]).board(synced.Board)
	// Members see whose computer the board lives on, since the owner's own boards sit right beside it.
	view.ID, view.Name, view.URL = lanDeviceBoardID(device, board), view.Name+" · "+firstOf(synced.DeviceName, device), ""
	view.External = nil
	view.Summary.External, view.Summary.ExternalDown = 0, 0
	return view, nil
}

func (b *APIBackend) LANBoardView(ctx context.Context, id string) (api.BoardView, error) {
	cfg := b.options.Config.Snapshot()
	if device, board, ok := splitLANDeviceBoard(id); ok {
		return b.lanDeviceBoard(cfg, device, board)
	}
	view, err := b.boardView(ctx, cfg, id)
	// Other computers of the owner's account are not part of what a LAN viewer was granted.
	view.External = nil
	view.Summary.External, view.Summary.ExternalDown = 0, 0
	return view, err
}

func (b *APIBackend) LANMonitorDetail(ctx context.Context, board, id string) (api.MonitorDetail, error) {
	if device, _, ok := splitLANDeviceBoard(board); ok {
		value, err := b.lanDeviceMonitor(ctx, api.GetCloudDeviceMonitor, device, id)
		if err != nil {
			return api.MonitorDetail{}, err
		}
		cfg := b.options.Config.Snapshot()
		scope := newCloudGuestScope(b.options.Cloud, device, cfg.Settings.LAN.WebDeviceBoards[device], cfg.Settings.LAN.WebDeviceServices[device])
		detail, ok := value.(api.MonitorDetail)
		if !ok {
			return api.MonitorDetail{}, fmt.Errorf("服务详情格式异常")
		}
		return scope.detail(detail), nil
	}
	value, err := b.monitorDetail(ctx, b.options.Config.Snapshot(), id)
	if err != nil {
		return api.MonitorDetail{}, err
	}
	detail, ok := value.(api.MonitorDetail)
	if !ok {
		return api.MonitorDetail{}, fmt.Errorf("服务详情格式异常")
	}
	return detail, nil
}

func (b *APIBackend) LANMonitorDoc(ctx context.Context, board, id string) (api.MonitorDoc, error) {
	if device, _, ok := splitLANDeviceBoard(board); ok {
		value, err := b.lanDeviceMonitor(ctx, api.GetCloudDeviceDoc, device, id)
		if err != nil {
			return api.MonitorDoc{}, err
		}
		doc, ok := value.(api.MonitorDoc)
		if !ok {
			return api.MonitorDoc{}, fmt.Errorf("文档格式异常")
		}
		return doc, nil
	}
	return b.monitorDoc(b.options.Config.Snapshot(), id)
}

// lanDeviceMonitor reads a service of another device through the cloud, as its owner would.
func (b *APIBackend) lanDeviceMonitor(ctx context.Context, op api.Operation, device, monitor string) (any, error) {
	if b.options.Cloud == nil {
		return nil, missingAPI("这块面板没有开放到内网")
	}
	return b.cloudOwnerOperation(ctx, op, api.Request{Params: map[string]string{"deviceId": device, "monitorId": monitor}})
}

func (b *APIBackend) lanOperation(ctx context.Context, op api.Operation, r api.Request) (any, error) {
	if b.options.Teams == nil {
		return nil, &api.Fault{Status: 503, Code: "lan_unavailable", Message: "内网共享需要以 monitor 或 both 运行"}
	}
	if op == api.GetLAN {
		return b.options.Teams.LANState(ctx)
	}
	var input api.LANConfigInput
	if err := api.DecodeBody(r.Body, &input); err != nil {
		return nil, err
	}
	if input.Peers != nil {
		peers, err := cleanLANPeers(*input.Peers)
		if err != nil {
			return nil, err
		}
		input.Peers = &peers
	}
	settings := b.options.Config.Snapshot().Settings.LAN
	if input.Name != nil {
		// The name others see is the account's nickname now; lan.name only seeds a new account.
		if _, err := b.options.Teams.saveProfile(ctx, api.ProfileInput{Nickname: input.Name}); err != nil {
			return nil, err
		}
		if input.Visible == nil && input.Web == nil && input.WebBoards == nil && input.WebServices == nil && input.WebDeviceBoards == nil && input.WebDeviceServices == nil && input.Peers == nil {
			return b.options.Teams.LANState(ctx)
		}
	}
	if input.Visible != nil {
		settings.Visible = *input.Visible
	}
	if settings.Visible {
		if err := b.options.Teams.EnsureLANTeam(ctx); err != nil {
			return nil, err
		}
	}
	updated, err := b.options.Editor.Update(ctx, "settings.yaml", func(old []byte) ([]byte, error) {
		latest, err := config.ParseSettings(old)
		if err != nil {
			return nil, err
		}
		return config.SetLAN(old, applyLANInput(latest.LAN, input))
	})
	if err != nil {
		return nil, err
	}
	settings = updated.Settings.LAN
	b.options.Teams.notify()
	// The member port restarts on the new address; give it a moment so the reply says how it went.
	for attempt := 0; attempt < 20; attempt++ {
		state, err := b.options.Teams.LANState(ctx)
		if err != nil || !settings.Visible || state.Listening && state.Problem == "" {
			return state, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return b.options.Teams.LANState(ctx)
}

// syncDepartmentFlag keeps settings' lan.departments true exactly while this computer hosts a department, which
// keeps the member port and discovery answering on the network for it.
func (b *APIBackend) syncDepartmentFlag(ctx context.Context) error {
	hosts := b.options.Teams.hostsDepartments(ctx)
	settings := b.options.Config.Snapshot().Settings.LAN
	if settings.Departments == hosts {
		return nil
	}
	settings.Departments = hosts
	if hosts {
		if err := b.options.Teams.EnsureLANTeam(ctx); err != nil {
			return err
		}
	}
	_, err := b.options.Editor.Update(ctx, "settings.yaml", func(old []byte) ([]byte, error) { return config.SetLAN(old, settings) })
	b.options.Teams.notify()
	return err
}

func cleanIDs(ids []string) []string {
	cleaned := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		cleaned = append(cleaned, id)
	}
	return cleaned
}

func applyLANInput(settings config.LANSettings, input api.LANConfigInput) config.LANSettings {
	if input.Visible != nil {
		settings.Visible = *input.Visible
	}
	if input.Web != nil {
		settings.Web = *input.Web
	}
	if input.WebBoards != nil {
		settings.WebBoards = cleanIDs(*input.WebBoards)
	}
	if input.WebServices != nil {
		if settings.WebServices == nil {
			settings.WebServices = map[string][]string{}
		}
		for board, ids := range *input.WebServices {
			if ids == nil {
				delete(settings.WebServices, board)
			} else {
				settings.WebServices[board] = ids
			}
		}
	}
	if input.WebDeviceBoards != nil {
		next := maps.Clone(settings.WebDeviceBoards)
		if next == nil {
			next = map[string][]string{}
		}
		for device, ids := range *input.WebDeviceBoards {
			cleaned := cleanIDs(ids)
			if len(cleaned) == 0 {
				delete(next, device)
			} else {
				next[device] = cleaned
			}
		}
		settings.WebDeviceBoards = next
		if len(next) == 0 {
			settings.WebDeviceBoards = nil
		}
	}
	if input.WebDeviceServices != nil {
		next := map[string]map[string][]string{}
		for device, boards := range settings.WebDeviceServices {
			next[device] = maps.Clone(boards)
		}
		for device, boards := range *input.WebDeviceServices {
			if next[device] == nil {
				next[device] = map[string][]string{}
			}
			for board, ids := range boards {
				if ids == nil {
					delete(next[device], board)
				} else {
					next[device][board] = cleanIDs(ids)
				}
			}
		}
		settings.WebDeviceServices = next
	}
	// A narrowed board that is no longer opened would come back narrowed the next time; drop it with the board.
	for device, boards := range settings.WebDeviceServices {
		for board := range boards {
			if !slices.Contains(settings.WebDeviceBoards[device], board) {
				delete(boards, board)
			}
		}
		if len(boards) == 0 {
			delete(settings.WebDeviceServices, device)
		}
	}
	if len(settings.WebDeviceServices) == 0 {
		settings.WebDeviceServices = nil
	}
	if input.Peers != nil {
		settings.Peers = *input.Peers
		if len(settings.Peers) == 0 {
			settings.Peers = nil
		}
	}
	return settings
}

func cleanLANPeers(values []string) ([]string, error) {
	result := []string{}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		host, err := config.LANPeerHost(value)
		if err != nil {
			return nil, invalidAPI(err.Error())
		}
		if !slices.Contains(result, host) {
			result = append(result, host)
		}
	}
	if len(result) > 32 {
		return nil, invalidAPI("最多记住 32 个跨网段地址")
	}
	return result, nil
}
