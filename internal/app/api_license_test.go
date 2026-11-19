package app

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/license"
)

// fakeLicenses signs a license for the one beta key it knows.
type fakeLicenses struct {
	key ed25519.PrivateKey
	now func() time.Time
}

func (f fakeLicenses) Post(_ context.Context, path, _ string, body any) (int, []byte, error) {
	input := body.(map[string]any)
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
	// An ordinary build never asks.
	f.backend.options.License = license.NewManager(license.Options{Home: t.TempDir(), Now: func() time.Time { return f.now }})
	if status := decodeAs[license.Status](t, f.request(t, "GET", "/api/v1/license", "read", "", 200).Body.Bytes()); status.State != "off" {
		t.Fatalf("ungated status=%+v", status)
	}
}
