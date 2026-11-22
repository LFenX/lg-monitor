// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/license"
)

// licenseOpen lists what answers before a beta key is entered: the gate itself, and data that keeps flowing in
// from agents and jobs so monitoring does not stop while someone looks for their key.
var licenseOpen = []api.Operation{api.GetLicense, api.ActivateLicense, api.LicenseGateSeen, api.LicenseAdmin, api.AgentPresence, api.ReportJobEvents}

func (b *APIBackend) licensed(op api.Operation) error {
	if b.options.License == nil || slices.Contains(licenseOpen, op) || b.options.License.Allowed() {
		return nil
	}
	return &api.Fault{Status: 403, Code: "license_required", Message: "这个 lgmon 需要内测密钥：在网页里输入管理员发给你的密钥后就能使用"}
}

func (b *APIBackend) licenseOperation(ctx context.Context, op api.Operation, r api.Request) (any, error) {
	gate := b.options.License
	if gate == nil && op == api.MarginEntry {
		return nil, marginFault(license.ErrNoLicense)
	}
	if gate == nil {
		return license.Status{State: "off"}, nil
	}
	switch op {
	case api.LicenseGateSeen:
		principal, _ := api.CurrentPrincipal(ctx)
		if !principal.Local || r.Via != "" {
			return nil, &api.Fault{Status: 403, Code: "local_only", Message: "只有本机密钥页可以登记"}
		}
		if err := gate.GateSeen(ctx); err != nil {
			return nil, licenseFault(err)
		}
		return map[string]bool{"ok": true}, nil
	case api.MarginEntry:
		ticket, err := gate.MarginTicket(ctx)
		if err != nil {
			return nil, marginFault(err)
		}
		return api.MarginEntryReply{URL: license.MarginURL(ticket)}, nil
	case api.GetLicense:
		// Opening a window is a good moment to notice a revoked key; it asks at most every few minutes.
		return gate.Check(ctx, true), nil
	case api.ActivateLicense:
		var input api.LicenseInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		status, err := gate.Activate(ctx, input.Key)
		if err != nil {
			return nil, licenseFault(err)
		}
		return status, nil
	}
	var input api.LicenseAdminInput
	if err := api.DecodeBody(r.Body, &input); err != nil {
		return nil, err
	}
	if input.Action == "adopt" {
		status, err := gate.Adopt(ctx, input.Session)
		if err != nil {
			return nil, licenseFault(err)
		}
		return status, nil
	}
	method, path, body := http.MethodPost, "", any(nil)
	switch input.Action {
	case "login":
		path, body = "/v1/admin/login", map[string]any{"password": input.Password}
	case "keys":
		method, path = http.MethodGet, "/v1/admin/keys"
	case "create":
		days, devices := 7, 1
		if input.Days != nil {
			days = *input.Days
		}
		if input.MaxDevices != nil {
			devices = *input.MaxDevices
		}
		path, body = "/v1/admin/keys", map[string]any{"label": input.Label, "days": days, "maxDevices": devices}
	case "update":
		change := map[string]any{}
		if input.Revoke {
			change["revoke"] = true
		}
		if input.Restore {
			change["restore"] = true
		}
		if input.ExtendDays != nil {
			change["extendDays"] = *input.ExtendDays
		}
		path, body = "/v1/admin/keys/"+url.PathEscape(input.ID), change
	case "remove_device":
		path, body = "/v1/admin/keys/"+url.PathEscape(input.ID)+"/devices/"+url.PathEscape(input.Device)+"/remove", map[string]any{}
	case "admin_devices":
		method, path = http.MethodGet, "/v1/admin/devices"
	case "remove_admin":
		if input.Device == "" {
			return nil, invalidAPI("缺少要移除的管理员设备")
		}
		path, body = "/v1/admin/devices/"+url.PathEscape(input.Device)+"/remove", map[string]any{}
	case "analytics":
		method, path = http.MethodGet, "/v1/admin/analytics"
	case "growth":
		method, path = http.MethodGet, "/v1/admin/growth"
	default:
		return nil, invalidAPI("不支持的管理操作")
	}
	status, data, err := gate.Admin(ctx, method, path, input.Session, body)
	if err != nil {
		return nil, &api.Fault{Status: 502, Code: "license_unreachable", Message: "连不上授权服务，请检查网络后再试", Cause: err}
	}
	if input.Action == "analytics" && status == http.StatusNotFound {
		// Older license servers without /v1/admin/analytics still work cleanly; the UI shows
		// real GitHub downloads + activation keys/devices and this admin machine's local milestones.
		return map[string]any{
			"telemetryReady": false,
			"devices":        []any{},
			"localUsage":     gate.LocalUsage(),
		}, nil
	}
	var reply map[string]any
	if json.Unmarshal(data, &reply) != nil {
		return nil, &api.Fault{Status: 502, Code: "license_unreachable", Message: "授权服务回复无效"}
	}
	if status != http.StatusOK {
		message := "授权服务拒绝了这个操作"
		if fault, ok := reply["error"].(map[string]any); ok {
			if text, ok := fault["message"].(string); ok && text != "" {
				message = text
			}
		}
		return nil, &api.Fault{Status: status, Code: "license_admin", Message: message}
	}
	if input.Action == "analytics" {
		if _, ok := reply["telemetryReady"]; !ok {
			reply["telemetryReady"] = true
		}
		reply["localUsage"] = gate.LocalUsage()
	}
	if input.Action == "remove_admin" {
		gate.ForgetAdmin(input.Device)
	}
	return reply, nil
}

// marginFault says why 留白 opens at its login page instead; the page falls back to the plain address.
func marginFault(err error) error {
	switch {
	case errors.Is(err, license.ErrNoLicense):
		return &api.Fault{Status: 404, Code: "license_none", Message: "这台电脑没有内测许可，留白需要登录"}
	case errors.Is(err, license.ErrRejected):
		return &api.Fault{Status: 403, Code: "license_rejected", Message: strings.TrimSuffix(err.Error(), "："+license.ErrRejected.Error())}
	}
	return &api.Fault{Status: 502, Code: "license_unreachable", Message: "连不上授权服务，留白需要登录", Cause: err}
}

func licenseFault(err error) error {
	if errors.Is(err, license.ErrRejected) {
		return &api.Fault{Status: 400, Code: "license_rejected", Message: strings.TrimSuffix(err.Error(), "："+license.ErrRejected.Error())}
	}
	return &api.Fault{Status: 502, Code: "license_unreachable", Message: "连不上授权服务，请检查网络后再试", Cause: err}
}

// UsageSnapshot computes anonymous local product milestones (no service names, URLs, or secrets)
// reported alongside license heartbeats for the author's admin growth funnel dashboard.
func (b *APIBackend) UsageSnapshot() license.UsageSnapshot {
	cfg := b.options.Config.Snapshot()
	snap := license.UsageSnapshot{
		BoardsCount:     len(cfg.Boards),
		MonitorsCount:   len(cfg.Targets),
		HasFirstBoard:   len(cfg.Boards) > 0,
		HasFirstMonitor: len(cfg.Targets) > 0,
	}
	features := make([]string, 0, 10)
	add := func(name string) {
		if !slices.Contains(features, name) {
			features = append(features, name)
		}
	}
	if len(cfg.Docs) > 0 {
		add("docs")
	}
	for _, target := range cfg.Targets {
		if len(target.Depends) > 0 {
			add("deps")
		}
		if target.Job != nil {
			add("batch_jobs")
		}
	}
	if len(cfg.Rules) > 0 {
		add("alerts")
	}
	if len(cfg.FeishuApps) > 0 || slices.ContainsFunc(cfg.Notifiers, func(n config.Notifier) bool {
		return strings.HasPrefix(n.Type, "feishu")
	}) {
		add("feishu")
	}
	b.agent.mu.Lock()
	seenAgent := b.agent.seenAt > 0
	b.agent.mu.Unlock()
	if seenAgent {
		add("agent_mcp")
	}
	if b.secret(context.Background(), config.DeepSeekKeyRef) != "" {
		add("bohemia")
	}
	if cfg.Settings.LAN.Visible {
		add("lan_share")
	}
	if cfg.Settings.LAN.Web {
		add("lan_web")
	}
	if cfg.Settings.LAN.Departments {
		add("departments")
	}
	slices.Sort(features)
	snap.Features = features
	return snap
}
