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

// LANBoardList and LANBoardView let the member port show boards without the rest of the API.
func (b *APIBackend) LANBoardList(ctx context.Context) ([]api.BoardInfo, error) {
	return b.boardList(ctx, b.options.Config.Snapshot())
}

func (b *APIBackend) LANBoardView(ctx context.Context, id string) (api.BoardView, error) {
	view, err := b.boardView(ctx, b.options.Config.Snapshot(), id)
	// Other computers of the owner's account are not part of what a LAN viewer was granted.
	view.External = nil
	view.Summary.External, view.Summary.ExternalDown = 0, 0
	return view, err
}

func (b *APIBackend) LANMonitorDetail(ctx context.Context, id string) (api.MonitorDetail, error) {
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

func (b *APIBackend) LANMonitorDoc(ctx context.Context, id string) (api.MonitorDoc, error) {
	return b.monitorDoc(b.options.Config.Snapshot(), id)
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
		if input.Visible == nil && input.Web == nil && input.WebBoards == nil && input.WebServices == nil && input.WebDeviceBoards == nil && input.Peers == nil {
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
