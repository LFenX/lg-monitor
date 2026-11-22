// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/LFenX/lg-monitor/internal/store"
)

type LANKeyStore interface {
	LANWebKey(context.Context) (store.LANWebKey, error)
	SetLANWebKey(context.Context, store.LANWebKey) error
	ChangeLANKeyExpiry(context.Context, int64) (bool, error)
}

type LANKeyStatus struct {
	Configured bool  `json:"configured"`
	Active     bool  `json:"active"`
	ExpiresAt  int64 `json:"expiresAt"`
	OnHost     bool  `json:"onHost"`
}

type LANKeyInput struct {
	ExpiresAt *int64 `json:"expiresAt" jsonschema:"minimum=0" jsonschema_description:"到期时间，UTC Unix 毫秒；0 表示长期有效。"`
}

type LANUnlockInput struct {
	Key string `json:"key" jsonschema:"maxLength=256"`
}

type LANKeyResult struct {
	LANKeyStatus
	Key string `json:"key"`
}

func isLANKeyOperation(op Operation) bool {
	return op == GetLANKey || op == GenerateLANKey || op == ChangeLANKeyExpiry || op == RevokeLANKey || op == UnlockLAN
}

func liveLANKey(key store.LANWebKey, now time.Time) bool {
	return len(key.Hash) == sha256.Size && (key.ExpiresAt == 0 || now.UnixMilli() < key.ExpiresAt)
}

const lanSessionPrefix = "lgmon-lan-session."

// The browser retains only a bearer for this peer and monitor process. Replacing the stored
// digest changes every derived bearer without persisting browser sessions or the shared key.
func (a *Authenticator) lanSession(key store.LANWebKey, ip string) string {
	mac := hmac.New(sha256.New, []byte(a.sessionSecret))
	mac.Write(key.Hash)
	mac.Write([]byte(ip))
	return lanSessionPrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Authenticator) authenticateLANSession(r *http.Request, token string) (Principal, bool, error) {
	ip, direct := lanVisitorRequest(r)
	if !direct || !a.LANWeb() || a.lanKeys == nil {
		return Principal{}, false, nil
	}
	key, err := a.lanKeys.LANWebKey(r.Context())
	if err != nil {
		return Principal{}, false, err
	}
	if !liveLANKey(key, a.now()) || subtle.ConstantTimeCompare([]byte(token), []byte(a.lanSession(key, ip))) != 1 {
		return Principal{}, false, nil
	}
	return Principal{Name: "lan-full-access", Scope: ScopeAdmin, LANFull: true}, true, nil
}

func (a *Authenticator) lanKeyOperation(r *http.Request, op Operation, body []byte) (any, error) {
	local := localRequest(r)
	if (op == GenerateLANKey || op == ChangeLANKeyExpiry || op == RevokeLANKey) && !local {
		return nil, &Fault{Status: 403, Code: "local_only", Message: "请在监控电脑本机的内网页面管理密钥"}
	}
	if a.lanKeys == nil {
		return nil, &Fault{Status: 503, Code: "lan_unavailable", Message: "内网完全访问密钥存储不可用"}
	}
	ctx := r.Context()
	switch op {
	case GenerateLANKey, ChangeLANKeyExpiry:
		var input LANKeyInput
		if err := DecodeBody(body, &input); err != nil {
			return nil, err
		}
		if input.ExpiresAt == nil || (*input.ExpiresAt != 0 && *input.ExpiresAt <= a.now().UnixMilli()) {
			return nil, &Fault{Status: 400, Code: "invalid_expiry", Message: "请选择未来的到期时间，长期有效请填 0"}
		}
		if op == ChangeLANKeyExpiry {
			updated, err := a.lanKeys.ChangeLANKeyExpiry(ctx, *input.ExpiresAt)
			if err != nil {
				return nil, err
			}
			if !updated {
				return nil, &Fault{Status: 409, Code: "lan_key_inactive", Message: "密钥已失效，请生成新密钥"}
			}
			return Confirmation{true, "已更新密钥有效期"}, nil
		}
		key := "lgmon_lankey_" + rand.Text()
		digest := TokenHash(key)
		record := store.LANWebKey{Hash: digest[:], ExpiresAt: *input.ExpiresAt}
		if err := a.lanKeys.SetLANWebKey(ctx, record); err != nil {
			return nil, err
		}
		return LANKeyResult{LANKeyStatus{Configured: true, Active: true, ExpiresAt: record.ExpiresAt, OnHost: true}, key}, nil
	case RevokeLANKey:
		if err := a.lanKeys.SetLANWebKey(ctx, store.LANWebKey{}); err != nil {
			return nil, err
		}
		return Confirmation{true, "已撤销内网完全访问密钥"}, nil
	}
	record, err := a.lanKeys.LANWebKey(ctx)
	if err != nil {
		return nil, err
	}
	active := liveLANKey(record, a.now())
	if op == GetLANKey {
		return LANKeyStatus{Configured: len(record.Hash) > 0, Active: active, ExpiresAt: record.ExpiresAt, OnHost: local}, nil
	}
	ip, direct := lanVisitorRequest(r)
	principal, _ := CurrentPrincipal(ctx)
	if !direct || !principal.Guest || !a.LANWeb() {
		return nil, &Fault{Status: 403, Code: "lan_guest_required", Message: "请从已开启网页访问的内网访客页面解锁"}
	}
	var input LANUnlockInput
	if err := DecodeBody(body, &input); err != nil {
		return nil, err
	}
	digest := TokenHash(input.Key)
	if !active || subtle.ConstantTimeCompare(digest[:], record.Hash) != 1 {
		return nil, &Fault{Status: 403, Code: "invalid_lan_key", Message: "密钥无效或已过期，请向主机管理员确认"}
	}
	return SessionResult{Token: a.lanSession(record, ip), Scope: ScopeAdmin}, nil
}
