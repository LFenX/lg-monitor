// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/cloud"
	"github.com/LFenX/lg-monitor/internal/license"
)

// fakeLicenses signs a license for the one beta key it knows.
type fakeLicenses struct {
	key        ed25519.PrivateKey
	now        func() time.Time
	adminLogin func(body map[string]any) (int, []byte)
}

func (f fakeLicenses) Post(_ context.Context, path, _ string, body any) (int, []byte, error) {
	input := body.(map[string]any)
	if path == "/v1/admin/login" && f.adminLogin != nil {
		code, data := f.adminLogin(input)
		return code, data, nil
	}
	if path == "/v1/margin" {
		text := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"aud":"margin","jti":"ZmFrZS10aWNrZXQtMDAwMQ"}`))
		return 200, []byte(`{"ticket":"` + text + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.key, []byte(text))) + `"}`), nil
	}
	if path != "/v1/activate" || input["key"] != "LGMON-FAKE1-FAKE2-FAKE3-FAKE4" {
		return 404, []byte(`{"error":{"code":"key_unknown","message":"这个内测密钥不存在，请核对后重新输入"}}`), nil
	}
	now := f.now().UnixMilli()
	data, _ := json.Marshal(license.Token{Version: 1, Key: "k_fake", Device: input["device"].(license.Device).ID, Label: "虚构同事", Expires: now + 86400000, Renew: now + 3600000, Issued: now})
	text := base64.RawURLEncoding.EncodeToString(data)
	signed, _ := json.Marshal(map[string]string{"token": text + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.key, []byte(text)))})
	return 200, signed, nil
}

func (fakeLicenses) Get(context.Context, string, string) (int, []byte, error) {
	return 200, []byte(`{}`), nil
}

func TestGatedBuildAnswersOnlyTheGateUntilAKeyIsEntered(t *testing.T) {
	f := newAPIFixture(t)
	public, private, _ := ed25519.GenerateKey(nil)
	on := true
	f.backend.options.License = license.NewManager(license.Options{Home: t.TempDir(), Client: fakeLicenses{key: private, now: func() time.Time { return f.now }}, Now: func() time.Time { return f.now }, Enforce: &on, PublicKey: public})
	if refused := f.request(t, "GET", "/api/v1/monitors", "admin", "", 403); !strings.Contains(refused.Body.String(), "license_required") {
		t.Fatal(refused.Body.String())
	}
	status := decodeAs[license.Status](t, f.request(t, "GET", "/api/v1/license", "read", "", 200).Body.Bytes())
	if !status.Enforced || status.State != "missing" {
		t.Fatalf("status=%+v", status)
	}
	if wrong := f.request(t, "POST", "/api/v1/license", "read", `{"key":"LGMON-WRONG"}`, 400); !strings.Contains(wrong.Body.String(), "不存在") || strings.Contains(wrong.Body.String(), "license rejected") {
		t.Fatal(wrong.Body.String())
	}
	if status := decodeAs[license.Status](t, f.request(t, "POST", "/api/v1/license", "read", `{"key":"LGMON-FAKE1-FAKE2-FAKE3-FAKE4"}`, 200).Body.Bytes()); status.State != "active" || status.Label != "虚构同事" {
		t.Fatalf("activated=%+v", status)
	}
	f.request(t, "GET", "/api/v1/monitors", "read", "", 200)
	// A licensed computer opens 留白 through its lgmon entry.
	entry := decodeAs[map[string]string](t, f.request(t, "POST", "/api/v1/license/margin", "read", "", 200).Body.Bytes())
	if !license.IsMarginURL(entry["url"]) {
		t.Fatalf("entry=%v", entry)
	}
	// An ordinary build never asks, and without a license 留白 keeps its login page.
	f.backend.options.License = license.NewManager(license.Options{Home: t.TempDir(), Now: func() time.Time { return f.now }})
	if status := decodeAs[license.Status](t, f.request(t, "GET", "/api/v1/license", "read", "", 200).Body.Bytes()); status.State != "off" {
		t.Fatalf("ungated status=%+v", status)
	}
	if none := f.request(t, "POST", "/api/v1/license/margin", "read", "", 404); !strings.Contains(none.Body.String(), "license_none") {
		t.Fatal(none.Body.String())
	}
}

type stubWhitelistCloudClient struct {
	email string
	token string
}

func (c stubWhitelistCloudClient) Do(_ context.Context, method, _, path, _, _ string, _ any, _ bool) (cloud.ClientResult, error) {
	if method == http.MethodPost && path == "/v1/auth/otp/verify" {
		b, _ := json.Marshal(map[string]any{
			"token": c.token,
			"user":  map[string]any{"id": "u_admin", "email": c.email},
		})
		return cloud.ClientResult{Status: 200, Body: b}, nil
	}
	return cloud.ClientResult{Status: 200, Body: []byte(`{"ok":true}`)}, nil
}

func TestWhitelistedCloudAccountAutoLogsIntoLicenseAdmin(t *testing.T) {
	f := newAPIFixture(t)
	var seenLogin map[string]any
	f.backend.options.License = license.NewManager(license.Options{
		Home: t.TempDir(),
		Now:  func() time.Time { return f.now },
		Client: fakeLicenses{
			now: func() time.Time { return f.now },
			adminLogin: func(body map[string]any) (int, []byte) {
				seenLogin = body
				if body["cloudToken"] == "fake-cloud-token" && body["email"] == "fli.gda@foxmail.com" {
					return 200, []byte(`{"session":"fake-admin-session","expiresAt":9999999999}`)
				}
				return 401, []byte(`{"error":{"message":"密码不对"}}`)
			},
		},
	})

	// Without a whitelisted cloud account, auto_login is rejected and WhitelistAdmin is false.
	status := decodeAs[license.Status](t, f.request(t, "GET", "/api/v1/license", "read", "", 200).Body.Bytes())
	if status.WhitelistAdmin {
		t.Fatalf("expected WhitelistAdmin=false before login, got %+v", status)
	}
	f.request(t, "POST", "/api/v1/license/admin", "read", `{"action":"auto_login"}`, 401)

	// Log into a non-whitelisted cloud account: still not whitelisted.
	otherVault := &apiVault{values: map[string][]byte{}}
	otherCloud := cloud.NewManager(cloud.Options{
		Home:    t.TempDir(),
		Version: "0.18.0",
		Secrets: otherVault,
		Client:  stubWhitelistCloudClient{email: "other@example.com", token: "other-token"},
		Now:     func() time.Time { return f.now },
	})
	if _, err := otherCloud.VerifyOTP(context.Background(), "other@example.com", "123456", "OtherPC", true); err != nil {
		t.Fatalf("VerifyOTP other: %v", err)
	}
	f.backend.options.Cloud = otherCloud
	status = decodeAs[license.Status](t, f.request(t, "GET", "/api/v1/license", "read", "", 200).Body.Bytes())
	if status.WhitelistAdmin {
		t.Fatalf("expected WhitelistAdmin=false for other@example.com, got %+v", status)
	}
	f.request(t, "POST", "/api/v1/license/admin", "read", `{"action":"auto_login"}`, 401)

	// Log into whitelisted fli.gda@foxmail.com: WhitelistAdmin is true and auto_login succeeds without a password.
	adminVault := &apiVault{values: map[string][]byte{}}
	adminCloud := cloud.NewManager(cloud.Options{
		Home:    t.TempDir(),
		Version: "0.18.0",
		Secrets: adminVault,
		Client:  stubWhitelistCloudClient{email: "fli.gda@foxmail.com", token: "fake-cloud-token"},
		Now:     func() time.Time { return f.now },
	})
	if _, err := adminCloud.VerifyOTP(context.Background(), "fli.gda@foxmail.com", "123456", "AdminPC", true); err != nil {
		t.Fatalf("VerifyOTP admin: %v", err)
	}
	f.backend.options.Cloud = adminCloud
	status = decodeAs[license.Status](t, f.request(t, "GET", "/api/v1/license", "read", "", 200).Body.Bytes())
	if !status.WhitelistAdmin {
		t.Fatalf("expected WhitelistAdmin=true for fli.gda@foxmail.com, got %+v", status)
	}
	reply := decodeAs[map[string]any](t, f.request(t, "POST", "/api/v1/license/admin", "read", `{"action":"auto_login"}`, 200).Body.Bytes())
	if reply["session"] != "fake-admin-session" {
		t.Fatalf("unexpected auto_login reply: %+v", reply)
	}
	if seenLogin["cloudToken"] != "fake-cloud-token" || seenLogin["email"] != "fli.gda@foxmail.com" {
		t.Fatalf("unexpected forwarded login body: %+v", seenLogin)
	}
}
