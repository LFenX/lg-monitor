// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/cloud"
	"github.com/LFenX/lg-monitor/internal/config"
)

// CloudIdentity implements cloud.SnapshotProvider using this computer's profile nickname and avatar.
func (b *APIBackend) CloudIdentity(ctx context.Context) (string, string) {
	if b.options.Teams != nil {
		if p, err := b.options.Teams.EnsureProfile(ctx); err == nil {
			return p.Nickname, p.Avatar
		}
	}
	return "", ""
}

// CloudSnapshot implements cloud.SnapshotProvider, returning boards, live monitors, and doc content hashes.
func (b *APIBackend) CloudSnapshot(ctx context.Context) ([]api.BoardInfo, []api.Monitor, map[string]string, error) {
	cfg := b.options.Config.Snapshot()
	boards, err := b.boardList(ctx, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	monitors, err := b.listMonitors(ctx, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	docHashes := make(map[string]string, len(cfg.Docs))
	for id, md := range cfg.Docs {
		if h := cloud.HashDoc(md); h != "" {
			docHashes[id] = h
		}
	}
	return boards, monitors, docHashes, nil
}

// CloudMonitorDoc implements cloud.SnapshotProvider, reading one monitor's Markdown doc on demand.
func (b *APIBackend) CloudMonitorDoc(_ context.Context, monitorID string) (string, error) {
	cfg := b.options.Config.Snapshot()
	return cfg.Docs[monitorID], nil
}

const cloudJobSyncDays = 30

func (b *APIBackend) cloudJobSnapshotForTarget(ctx context.Context, target config.Target) (api.JobDays, error) {
	days, err := b.jobDays(ctx, target, cloudJobSyncDays)
	if err != nil {
		return api.JobDays{}, err
	}
	info, err := b.jobInfo(ctx, target)
	if err != nil {
		return api.JobDays{}, err
	}
	if info.File != nil {
		// The collector touches updatedAt on every report; leaving it in would re-upload unchanged records.
		file := *info.File
		file.UpdatedAt = 0
		info.File = &file
	}
	return cloud.TrimJobDaysForSync(api.JobDays{Job: info, Days: days}, cloud.MaxJobSnapshotBytes), nil
}

// CloudJobHashes implements cloud.JobSnapshotProvider, returning deterministic content hashes for all batch jobs.
func (b *APIBackend) CloudJobHashes(ctx context.Context) (map[string]string, error) {
	cfg := b.options.Config.Snapshot()
	var hashes map[string]string
	for _, target := range cfg.Targets {
		if target.Job == nil {
			continue
		}
		snap, err := b.cloudJobSnapshotForTarget(ctx, target)
		if err != nil {
			return nil, err
		}
		if hashes == nil {
			hashes = make(map[string]string)
		}
		if h := cloud.HashJobDays(snap); h != "" {
			hashes[target.ID] = h
		}
	}
	return hashes, nil
}

// CloudJobSnapshot implements cloud.JobSnapshotProvider, building one batch job's 30-day sync snapshot on demand.
func (b *APIBackend) CloudJobSnapshot(ctx context.Context, monitorID string) (api.JobDays, bool, error) {
	cfg := b.options.Config.Snapshot()
	target := targetIndex(cfg)[monitorID]
	if target.ID == "" || target.Job == nil {
		return api.JobDays{}, false, nil
	}
	snap, err := b.cloudJobSnapshotForTarget(ctx, target)
	if err != nil {
		return api.JobDays{}, false, err
	}
	return snap, true, nil
}

func toAPICloudStatus(s cloud.Status) api.CloudStatus {
	return api.CloudStatus{
		LoggedIn:     s.LoggedIn,
		Email:        s.Email,
		HasPassword:  s.HasPassword,
		DeviceID:     s.DeviceID,
		DeviceName:   s.DeviceName,
		ServerURL:    s.ServerURL,
		Paused:       s.Paused,
		Syncing:      s.Syncing,
		LastPushAt:   s.LastPushAt,
		LastPullAt:   s.LastPullAt,
		LastError:    s.LastError,
		DevicesCount: s.DevicesCount,
		Stats: api.CloudSyncStats{
			BytesSent:      s.Stats.BytesSent,
			BytesReceived:  s.Stats.BytesReceived,
			PushCount:      s.Stats.PushCount,
			HeartbeatCount: s.Stats.HeartbeatCount,
			PullCount:      s.Stats.PullCount,
			DocUploads:     s.Stats.DocUploads,
		},
	}
}

func cloudFault(err error) error {
	var re *cloud.RemoteError
	if errors.As(err, &re) {
		status := re.Status
		switch {
		case status == 401 || status == 403:
			// The sync service rejected a code, password or its own session. Passed on as 401, the panel
			// would take it for its own local session expiring.
			status = 400
		case status < 400 || status >= 600:
			status = 502
		}
		code := re.Code
		if code == "" {
			code = "cloud_error"
		}
		return &api.Fault{Status: status, Code: code, Message: re.Error(), Cause: err}
	}
	return &api.Fault{Status: 400, Code: "cloud_error", Message: err.Error(), Cause: err}
}

func (b *APIBackend) cloudOperation(ctx context.Context, op api.Operation, r api.Request) (any, error) {
	if p, ok := api.CurrentPrincipal(ctx); ok && p.Guest {
		return b.cloudGuestOperation(ctx, b.options.Config.Snapshot(), op, r)
	}
	return b.cloudOwnerOperation(ctx, op, r)
}

func (b *APIBackend) cloudOwnerOperation(ctx context.Context, op api.Operation, r api.Request) (any, error) {
	mgr := b.options.Cloud
	if mgr == nil {
		if op == api.GetCloudStatus {
			return api.CloudStatus{ServerURL: cloud.DefaultServer()}, nil
		}
		if op == api.ListCloudDevices {
			return []api.CloudDeviceSummary{}, nil
		}
		if op == api.ListExternalServices {
			return []api.ExternalCandidate{}, nil
		}
		return nil, &api.Fault{Status: 503, Code: "cloud_unavailable", Message: "当前运行模式未启用云同步"}
	}

	switch op {
	case api.GetCloudStatus:
		return toAPICloudStatus(mgr.Status(ctx)), nil

	case api.CloudRequestOTP:
		var input api.CloudOTPRequestInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		res, err := mgr.RequestOTP(ctx, input.Email, input.Purpose, input.Lang)
		if err != nil {
			return nil, cloudFault(err)
		}
		return api.CloudOTPResult{OK: res.OK, CooldownSec: res.CooldownSec, ExpiresInSec: res.ExpiresInSec}, nil

	case api.CloudVerifyOTP:
		var input api.CloudOTPVerifyInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		st, err := mgr.VerifyOTP(ctx, input.Email, input.Code, input.DeviceName)
		if err != nil {
			return nil, cloudFault(err)
		}
		return toAPICloudStatus(st), nil

	case api.CloudRegister:
		var input api.CloudRegisterInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		st, err := mgr.Register(ctx, input.Email, input.Code, input.Password, input.DeviceName)
		if err != nil {
			return nil, cloudFault(err)
		}
		return toAPICloudStatus(st), nil

	case api.CloudLoginPassword:
		var input api.CloudPasswordLoginInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		st, err := mgr.LoginPassword(ctx, input.Email, input.Password, input.DeviceName)
		if err != nil {
			return nil, cloudFault(err)
		}
		return toAPICloudStatus(st), nil

	case api.CloudSetPassword:
		var input api.CloudSetPasswordInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		st, err := mgr.SetPassword(ctx, input.CurrentPassword, input.NewPassword)
		if err != nil {
			return nil, cloudFault(err)
		}
		return toAPICloudStatus(st), nil

	case api.CloudResetPassword:
		var input api.CloudResetPasswordInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		if err := mgr.ResetPassword(ctx, input.Email, input.Code, input.NewPassword); err != nil {
			return nil, cloudFault(err)
		}
		return api.Confirmation{OK: true, Message: "密码已重置，现在可以使用新密码登录"}, nil

	case api.CloudLogout:
		var input api.CloudLogoutInput
		if len(r.Body) > 0 {
			if err := api.DecodeBody(r.Body, &input); err != nil {
				return nil, err
			}
		}
		st, err := mgr.Logout(ctx, input.UnbindDevice)
		if err != nil {
			return nil, cloudFault(err)
		}
		return toAPICloudStatus(st), nil

	case api.UpdateCloudSettings:
		var input api.CloudSettingsInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		st, err := mgr.SetSettings(ctx, input.ServerURL, input.Paused, input.DeviceName)
		if err != nil {
			return nil, cloudFault(err)
		}
		return toAPICloudStatus(st), nil

	case api.TriggerCloudSync:
		st, err := mgr.SyncNow(ctx)
		if err != nil {
			return nil, cloudFault(err)
		}
		return toAPICloudStatus(st), nil

	case api.ListCloudDevices:
		raw := mgr.Devices()
		out := make([]api.CloudDeviceSummary, 0, len(raw))
		for _, d := range raw {
			out = append(out, api.CloudDeviceSummary{
				ID:           d.ID,
				Name:         d.Name,
				Avatar:       d.Avatar,
				OS:           d.OS,
				Version:      d.Version,
				IsCurrent:    d.IsCurrent,
				Online:       d.Online,
				LastSeen:     d.LastSeen,
				StateUpdated: d.StateUpdated,
				TotalCount:   d.TotalCount,
				DownCount:    d.DownCount,
				Boards:       d.Boards,
			})
		}
		return out, nil

	case api.ListExternalServices:
		return mgr.Services(), nil

	case api.RenameCloudDevice:
		var input api.CloudRenameDeviceInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		if err := mgr.RenameDevice(ctx, r.Params["deviceId"], input.Name); err != nil {
			return nil, cloudFault(err)
		}
		return api.Confirmation{OK: true, Message: "设备名称已更新"}, nil

	case api.UnbindCloudDevice:
		if err := mgr.UnbindDevice(ctx, r.Params["deviceId"]); err != nil {
			return nil, cloudFault(err)
		}
		return api.Confirmation{OK: true, Message: "设备已解绑"}, nil

	case api.GetCloudDeviceBoard:
		view, err := mgr.DeviceBoard(r.Params["deviceId"], r.Params["boardId"])
		if err != nil {
			return nil, missingAPI(err.Error())
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

	case api.GetCloudDeviceMonitor:
		detail, err := mgr.DeviceMonitor(r.Params["deviceId"], r.Params["monitorId"])
		if err != nil {
			return nil, missingAPI(err.Error())
		}
		return detail, nil

	case api.GetCloudDeviceDoc:
		md, err := mgr.DeviceMonitorDoc(ctx, r.Params["deviceId"], r.Params["monitorId"])
		if err != nil {
			return nil, cloudFault(err)
		}
		return api.MonitorDoc{ID: r.Params["monitorId"], Markdown: md}, nil

	case api.GetCloudDeviceJobDays:
		n := 14
		if text := r.Query.Get("days"); text != "" {
			value, err := strconv.Atoi(text)
			if err != nil || value < 1 || value > 90 {
				return nil, invalidAPI("days 必须是 1～90")
			}
			n = value
		}
		res, err := mgr.DeviceJobDays(ctx, r.Params["deviceId"], r.Params["monitorId"], n)
		if err != nil {
			return nil, cloudFault(err)
		}
		return res, nil

	case api.GetCloudDeviceJobDay:
		date := r.Params["date"]
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			return nil, invalidAPI("业务日期必须是北京时间的 YYYY-MM-DD")
		}
		res, err := mgr.DeviceJobDay(ctx, r.Params["deviceId"], r.Params["monitorId"], date)
		if err != nil {
			return nil, cloudFault(err)
		}
		return res, nil
	}

	return nil, missingAPI("未知的云同步接口")
}
