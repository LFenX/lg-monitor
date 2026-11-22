// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

import "github.com/LFenX/lg-monitor/internal/license"

// The beta gate: a release built to ask for a beta key answers only these until one is entered.
const (
	GetLicense      Operation = "license"
	ActivateLicense Operation = "license_activate"
	LicenseGateSeen Operation = "license_gate_seen"
	LicenseAdmin    Operation = "license_admin"
	MarginEntry     Operation = "license_margin"
)

// MarginEntryReply is where 留白 lets this computer's person in without its login page.
type MarginEntryReply struct {
	URL string `json:"url" jsonschema_description:"留白的 lgmon 入口地址，带一张 5 分钟内有效、只能用一次的凭证。"`
}

type LicenseInput struct {
	Key string `json:"key" jsonschema:"minLength=1,maxLength=200" jsonschema_description:"管理员发给你的内测密钥，例如 LGMON-XXXXX-XXXXX-XXXXX-XXXXX。"`
}

// LicenseAdminInput is one request of the owner's console, passed on to the license service.
type LicenseAdminInput struct {
	Action     string `json:"action" jsonschema:"enum=login,enum=keys,enum=create,enum=update,enum=remove_device,enum=adopt,enum=admin_devices,enum=remove_admin,enum=analytics,enum=growth"`
	Password   string `json:"password,omitempty" jsonschema:"writeOnly=true,maxLength=200"`
	Session    string `json:"session,omitempty" jsonschema:"maxLength=2000"`
	ID         string `json:"id,omitempty" jsonschema:"maxLength=80"`
	Device     string `json:"device,omitempty" jsonschema:"maxLength=80"`
	Label      string `json:"label,omitempty" jsonschema:"maxLength=60"`
	Days       *int   `json:"days,omitempty"`
	MaxDevices *int   `json:"maxDevices,omitempty"`
	ExtendDays *int   `json:"extendDays,omitempty"`
	Revoke     bool   `json:"revoke,omitempty"`
	Restore    bool   `json:"restore,omitempty"`
}

func licenseRoutes() []Route {
	return []Route{
		{LicenseGateSeen, "POST", "/api/v1/license/gate-seen", "登记已安装电脑实际打开内测密钥页", ScopeRead, nil, map[string]bool{}, nil},
		{GetLicense, "GET", "/api/v1/license", "内测授权：这个版本是否要求内测密钥、这台电脑是否已激活", ScopeRead, nil, license.Status{}, nil},
		{ActivateLicense, "POST", "/api/v1/license", "输入内测密钥激活这台电脑", ScopeRead, LicenseInput{}, license.Status{}, nil},
		{LicenseAdmin, "POST", "/api/v1/license/admin", "内测管理（隐藏入口）：管理员登录、列出和创建内测密钥、收回密钥或电脑、把这台电脑设为管理员设备、列出和移除管理员设备", ScopeRead, LicenseAdminInput{}, map[string]any{}, nil},
		{MarginEntry, "POST", "/api/v1/license/margin", "打开留白：向授权服务要一张凭证，得到不用登录就能进入留白的地址", ScopeRead, nil, MarginEntryReply{}, nil},
	}
}
