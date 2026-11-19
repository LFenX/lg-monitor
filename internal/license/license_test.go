package license

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeService signs licenses the way the Worker does and answers checks from a switch the test flips.
type fakeService struct {
	key     ed25519.PrivateKey
	now     func() time.Time
	state   string
	offline bool
	devices []Device
}

func (f *fakeService) sign(device string, admin bool) string {
	now := f.now().UnixMilli()
	payload := Token{Version: 1, Key: "k_fake", Device: device, Label: "虚构同事", Expires: now + 30*24*3600*1000, Renew: now + 3*24*3600*1000, Issued: now}
	if admin {
		payload.Key, payload.Expires = "admin", 0
	}
	data, _ := json.Marshal(payload)
	body := base64.RawURLEncoding.EncodeToString(data)
	return body + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(f.key, []byte(body)))
}

func (f *fakeService) Post(_ context.Context, path, session string, body any) (int, []byte, error) {
	if f.offline {
		return 0, nil, errors.New("offline")
	}
	input := body.(map[string]any)
	device := input["device"].(Device)
	f.devices = append(f.devices, device)
	reply := func(value any) (int, []byte, error) {
		data, _ := json.Marshal(value)
		return 200, data, nil
	}
	switch path {
	case "/v1/activate":
		if input["key"] != "LGMON-FAKE1-FAKE2-FAKE3-FAKE4" {
			return 404, []byte(`{"error":{"code":"key_unknown","message":"这个内测密钥不存在，请核对后重新输入"}}`), nil
		}
		return reply(map[string]string{"token": f.sign(device.ID, false)})
	case "/v1/check":
		if f.state != "active" {
			return reply(map[string]string{"state": f.state})
		}
		return reply(map[string]string{"state": "active", "token": f.sign(device.ID, false)})
	case "/v1/admin/adopt":
		if session != "fake-session" {
			return 401, []byte(`{"error":{"code":"admin_required","message":"请先登录管理员"}}`), nil
		}
		return reply(map[string]string{"token": f.sign(device.ID, true)})
	}
	return 404, nil, nil
}

func (f *fakeService) Get(context.Context, string, string) (int, []byte, error) {
	return 200, []byte(`{}`), nil
}

func TestGateActivatesRenewsAndEnds(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(nil)
	clock := time.UnixMilli(2_000_000_000_000)
	now := func() time.Time { return clock }
	service := &fakeService{key: private, now: now, state: "active"}
	on := true
	home := t.TempDir()
	m := NewManager(Options{Home: home, Name: func() string { return "虚构电脑" }, Version: "0.13.2", Client: service, Now: now, Enforce: &on, PublicKey: public})
	if m.Allowed() || m.Status().State != "missing" {
		t.Fatalf("a new install must wait for a key: %+v", m.Status())
	}
	if _, err := m.Activate(context.Background(), "LGMON-WRONG"); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("wrong key: %v", err)
	}
	status, err := m.Activate(context.Background(), "LGMON-FAKE1-FAKE2-FAKE3-FAKE4")
	if err != nil || status.State != "active" || status.Label != "虚构同事" || !m.Allowed() {
		t.Fatalf("activated: %+v %v", status, err)
	}
	if name := service.devices[len(service.devices)-1]; name.Name != "虚构电脑" || !strings.HasPrefix(name.ID, "dev-") || name.Version != "0.13.2" {
		t.Fatalf("device=%+v", name)
	}
	// The license and the device survive a restart.
	again := NewManager(Options{Home: home, Client: service, Now: now, Enforce: &on, PublicKey: public})
	if !again.Allowed() || again.deviceID() != m.deviceID() {
		t.Fatal("license lost on restart")
	}
	// Offline, it lasts until it was due to be renewed, then stops.
	service.offline = true
	clock = clock.Add(31 * time.Minute)
	if status := m.Check(context.Background(), false); status.State != "active" || status.Problem == "" {
		t.Fatalf("offline check: %+v", status)
	}
	clock = clock.Add(3 * 24 * time.Hour)
	if m.Allowed() || m.Status().State != "stale" {
		t.Fatalf("stale license still allowed: %+v", m.Status())
	}
	// Back online, a check renews it; a revoked key ends it at the next check.
	service.offline = false
	if status := m.Check(context.Background(), true); status.State != "active" {
		t.Fatalf("renewed: %+v", status)
	}
	service.state = "revoked"
	clock = clock.Add(6 * time.Minute)
	if status := m.Check(context.Background(), true); status.State != "missing" || !strings.Contains(status.Problem, "收回") || m.Allowed() {
		t.Fatalf("revoked: %+v", status)
	}
}

func TestGateRejectsForeignOrForgedLicensesAndIsOffWithoutTheSwitch(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(nil)
	_, other, _ := ed25519.GenerateKey(nil)
	now := func() time.Time { return time.UnixMilli(2_000_000_000_000) }
	service := &fakeService{key: private, now: now, state: "active"}
	if _, err := Parse(service.sign("dev-a", false), "dev-b", public); !errors.Is(err, ErrRejected) {
		t.Fatalf("another computer's license: %v", err)
	}
	forged := (&fakeService{key: other, now: now}).sign("dev-a", false)
	if _, err := Parse(forged, "dev-a", public); !errors.Is(err, ErrRejected) {
		t.Fatalf("forged license: %v", err)
	}
	off := false
	m := NewManager(Options{Home: t.TempDir(), Client: service, Now: now, Enforce: &off, PublicKey: public})
	if !m.Allowed() || m.Status().State != "off" {
		t.Fatalf("an ordinary build must not be gated: %+v", m.Status())
	}
	on := true
	owner := NewManager(Options{Home: t.TempDir(), Client: service, Now: now, Enforce: &on, PublicKey: public})
	if _, err := owner.Adopt(context.Background(), "wrong"); err == nil {
		t.Fatal("adopted without an admin session")
	}
	if status, err := owner.Adopt(context.Background(), "fake-session"); err != nil || !status.Admin || status.State != "active" {
		t.Fatalf("adopted: %+v %v", status, err)
	}
	if Enforced() {
		t.Fatal("tests are built without the release switch")
	}
}
