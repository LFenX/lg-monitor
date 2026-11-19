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
	"github.com/LFenX/lg-monitor/internal/license"
)

// licenseOpen lists what answers before a beta key is entered: the gate itself, and data that keeps flowing in
// from agents and jobs so monitoring does not stop while someone looks for their key.
var licenseOpen = []api.Operation{api.GetLicense, api.ActivateLicense, api.LicenseAdmin, api.AgentPresence, api.ReportJobEvents}

func (b *APIBackend) licensed(op api.Operation) error {
	if b.options.License == nil || slices.Contains(licenseOpen, op) || b.options.License.Allowed() {
		return nil
	}
	return &api.Fault{Status: 403, Code: "license_required", Message: "这个 lgmon 需要内测密钥：在网页里输入管理员发给你的密钥后就能使用"}
}

func (b *APIBackend) licenseOperation(ctx context.Context, op api.Operation, r api.Request) (any, error) {
	gate := b.options.License
	if gate == nil {
		return license.Status{State: "off"}, nil
	}
	switch op {
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
	default:
		return nil, invalidAPI("不支持的管理操作")
	}
	status, data, err := gate.Admin(ctx, method, path, input.Session, body)
	if err != nil {
		return nil, &api.Fault{Status: 502, Code: "license_unreachable", Message: "连不上授权服务，请检查网络后再试", Cause: err}
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
	return reply, nil
}

func licenseFault(err error) error {
	if errors.Is(err, license.ErrRejected) {
		return &api.Fault{Status: 400, Code: "license_rejected", Message: strings.TrimSuffix(err.Error(), "："+license.ErrRejected.Error())}
	}
	return &api.Fault{Status: 502, Code: "license_unreachable", Message: "连不上授权服务，请检查网络后再试", Cause: err}
}
