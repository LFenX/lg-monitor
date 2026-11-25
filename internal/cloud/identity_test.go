// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func accepting(call scriptedCall) (int, any, string) {
	if status, body, etag, ok := signedInReply(call); ok {
		return status, body, etag
	}
	switch {
	case call.Path == "/v1/sync/push":
		return 200, SyncPushResponse{OK: true}, ""
	case strings.HasPrefix(call.Path, "/v1/sync/snapshot"):
		return 200, SyncSnapshotResponse{Seq: 1}, `"e1"`
	}
	return 200, map[string]any{"ok": true}, ""
}

func statePath(home string) string { return filepath.Join(home, "data", stateFileName) }

// A power cut once left the state file full of zeros: the computer was signed out and came back as a new device.
func TestDamagedStateFileKeepsTheDeviceAndTheSession(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }
	home, secrets := t.TempDir(), &fakeSecrets{}
	client := &scriptedClient{handle: accepting}
	before := newSignedIn(t, home, secrets, client, clock, oneMonitor(10)).Status(context.Background())

	for name, damage := range map[string]func() error{
		"zeros":   func() error { return os.WriteFile(statePath(home), make([]byte, 512), 0o600) },
		"cut off": func() error { return os.WriteFile(statePath(home), []byte(`{"deviceId": "dev-`), 0o600) },
		"missing": func() error { return os.Remove(statePath(home)) },
	} {
		if err := damage(); err != nil {
			t.Fatal(err)
		}
		after := NewManager(Options{Home: home, Secrets: secrets, Client: client, Now: clock}).Status(context.Background())
		if after.DeviceID != before.DeviceID || !after.LoggedIn || after.Email != before.Email {
			t.Fatalf("%s: before %+v, after %+v", name, before, after)
		}
		if st, err := readState(statePath(home)); err != nil || st.DeviceID != before.DeviceID {
			t.Fatalf("%s: the state file was not repaired: %+v %v", name, st, err)
		}
	}
}

func TestFirstStartOfThisVersionBacksUpAnExistingIdentity(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(statePath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(home), []byte(`{"deviceId":"dev-old","email":"me@example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(Options{Home: home})
	if st, err := readState(statePath(home) + ".bak"); err != nil || st.DeviceID != "dev-old" || mgr.CurrentDevice() != "dev-old" {
		t.Fatalf("backup %+v, %v", st, err)
	}
}

func TestSyncingDoesNotRewriteTheIdentityEveryTime(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	mgr := newSignedIn(t, t.TempDir(), &fakeSecrets{}, &scriptedClient{handle: accepting}, func() time.Time { return now }, oneMonitor(10))
	ctx := context.Background()
	saved := func() int64 {
		st, err := readState(mgr.path)
		if err != nil {
			t.Fatal(err)
		}
		return st.LastPushAt
	}
	for range 5 {
		now = now.Add(time.Minute)
		if err := mgr.PushOnce(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	if saved() != 0 || mgr.Status(ctx).LastPushAt != now.UnixMilli() {
		t.Fatalf("pushes should stay in memory for a while: on disk %d", saved())
	}
	now = now.Add(timesSaveEvery)
	if err := mgr.PushOnce(ctx, true); err != nil || saved() != now.UnixMilli() {
		t.Fatalf("the sync time should reach the disk eventually: %d, %v", saved(), err)
	}
}

func TestSignInTakesUpTheRecordTheAccountAlreadyHas(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	var device DeviceMetaInput
	client := &scriptedClient{handle: func(call scriptedCall) (int, any, string) {
		if call.Path != "/v1/auth/otp/verify" {
			return accepting(call)
		}
		var body struct {
			Device   DeviceMetaInput `json:"device"`
			Remember bool            `json:"remember"`
		}
		_ = json.Unmarshal(call.Body, &body)
		device = body.Device
		if !body.Remember {
			t.Error("the service should learn that this sign-in is remembered")
		}
		return 200, map[string]any{
			"token": "lgs_fake", "user": map[string]any{"id": "usr_1", "email": "me@example.com"},
			"device": map[string]any{"id": "dev-earlier", "name": "公司主机"}, "binding": "known",
		}, ""
	}}
	home, secrets := t.TempDir(), &fakeSecrets{}
	mgr := NewManager(Options{Home: home, Secrets: secrets, Client: client, Now: func() time.Time { return now }, MachineKey: func() string { return "machine-1" }})
	mgr.SetProvider(oneMonitor(10))
	st, err := mgr.VerifyOTP(context.Background(), "me@example.com", "123456", "公司主机", true)
	if err != nil || device.Machine != "machine-1" || st.DeviceID != "dev-earlier" || st.Binding != BindingKnown {
		t.Fatalf("status %+v, sent %+v, err %v", st, device, err)
	}
	if mgr.Status(context.Background()).Binding != "" {
		t.Fatal("binding belongs to the sign-in reply only")
	}
	if again := NewManager(Options{Home: home, Secrets: secrets}); again.CurrentDevice() != "dev-earlier" {
		t.Fatalf("the earlier ID was not kept: %s", again.CurrentDevice())
	}
}

func TestSessionOnlySignInEndsWithTheProcess(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }
	home, secrets := t.TempDir(), &fakeSecrets{}
	client := &scriptedClient{handle: accepting}
	ctx := context.Background()
	// An earlier remembered session must not come back either.
	first := newSignedIn(t, home, secrets, client, clock, oneMonitor(10))
	if !first.Status(ctx).Remember {
		t.Fatal("expected a remembered session")
	}
	st, err := first.VerifyOTP(ctx, "me@example.com", "123456", "公司主机", false)
	if err != nil || !st.LoggedIn || st.Remember {
		t.Fatalf("session-only sign-in: %+v %v", st, err)
	}
	if raw, _ := secrets.Get(ctx, sessionSecretKey); string(raw) != signedOutMarker {
		t.Fatalf("a session-only token reached the vault: %q", raw)
	}
	after := NewManager(Options{Home: home, Secrets: secrets, Client: client, Now: clock}).Status(ctx)
	if after.LoggedIn || after.Email != "" || after.LastEmail != "me@example.com" || after.DeviceID != st.DeviceID {
		t.Fatalf("after a restart: %+v", after)
	}
}

type flakySecrets struct {
	fakeSecrets
	fail bool
}

func (f *flakySecrets) Get(ctx context.Context, key string) ([]byte, error) {
	if f.fail {
		return nil, errors.New("DPAPI is not ready")
	}
	return f.fakeSecrets.Get(ctx, key)
}

func TestUnreadableVaultAtStartIsRetried(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }
	home, secrets := t.TempDir(), &flakySecrets{}
	client := &scriptedClient{handle: accepting}
	ctx := context.Background()
	newSignedIn(t, home, secrets, client, clock, oneMonitor(10))

	secrets.fail = true
	mgr := NewManager(Options{Home: home, Secrets: secrets, Client: client, Now: clock})
	if st := mgr.Status(ctx); st.LoggedIn || st.LastError == "" {
		t.Fatalf("expected a reason while the vault is unreadable: %+v", st)
	}
	secrets.fail = false
	if !mgr.restoreToken(ctx) {
		t.Fatal("the session should come back")
	}
	if st := mgr.Status(ctx); !st.LoggedIn || st.LastError != "" || st.Email != "me@example.com" {
		t.Fatalf("after the retry: %+v", st)
	}
}

func TestAProxysRefusalDoesNotSignOut(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	client := &scriptedClient{handle: func(call scriptedCall) (int, any, string) {
		if status, body, etag, ok := signedInReply(call); ok {
			return status, body, etag
		}
		return 401, "<html>portal</html>", ""
	}}
	mgr := newSignedIn(t, t.TempDir(), &fakeSecrets{}, client, func() time.Time { return now }, oneMonitor(10))
	if err := mgr.PushOnce(context.Background(), true); err == nil {
		t.Fatal("expected the push to fail")
	}
	if st := mgr.Status(context.Background()); !st.LoggedIn || st.LastError == "" {
		t.Fatalf("only the service may end a session: %+v", st)
	}
}

func TestMergeCarriesOnAsTheEarlierRecord(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	merged := false
	client := &scriptedClient{handle: func(call scriptedCall) (int, any, string) {
		if call.Method == "POST" && call.Path == "/v1/devices/dev-earlier/merge" {
			merged = true
			return 200, map[string]any{"ok": true, "id": "dev-earlier"}, ""
		}
		return accepting(call)
	}}
	home, secrets := t.TempDir(), &fakeSecrets{}
	mgr := newSignedIn(t, home, secrets, client, func() time.Time { return now }, oneMonitor(10))
	ctx := context.Background()
	if err := mgr.PushOnce(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := mgr.MergeDevice(ctx, "../dev"); err == nil || merged {
		t.Fatal("an invalid ID must be refused locally")
	}
	if err := mgr.MergeDevice(ctx, "dev-earlier"); err != nil || !merged {
		t.Fatalf("merge: %v", err)
	}
	if mgr.CurrentDevice() != "dev-earlier" || NewManager(Options{Home: home, Secrets: secrets}).CurrentDevice() != "dev-earlier" {
		t.Fatalf("device after merge: %s", mgr.CurrentDevice())
	}
	// Everything is sent again under the new ID.
	pushes := 0
	for _, call := range client.calls {
		var req SyncPushRequest
		if call.Path == "/v1/sync/push" && json.Unmarshal(call.Body, &req) == nil && req.Meta != nil && req.State != nil {
			pushes++
		}
	}
	if pushes != 2 {
		t.Fatalf("expected a full push before and after the merge, got %d", pushes)
	}
}

func TestRemovingThisDeviceFailsLoudlyWhenTheServiceIsUnreachable(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	client := &scriptedClient{handle: func(call scriptedCall) (int, any, string) {
		if call.Path == "/v1/auth/logout" {
			return 503, nil, ""
		}
		return accepting(call)
	}}
	mgr := newSignedIn(t, t.TempDir(), &fakeSecrets{}, client, func() time.Time { return now }, oneMonitor(10))
	ctx := context.Background()
	if err := mgr.UnbindDevice(ctx, mgr.CurrentDevice()); err == nil || !mgr.Status(ctx).LoggedIn {
		t.Fatalf("a record still in the account must not look removed: %v", err)
	}
	if _, err := mgr.Logout(ctx, false); err != nil || mgr.Status(ctx).LoggedIn {
		t.Fatalf("signing out must work without the service: %v", err)
	}
}

func TestHomeInstanceTellsDataDirectoriesApart(t *testing.T) {
	a, b := homeInstance(filepath.Join(t.TempDir(), "a")), homeInstance(filepath.Join(t.TempDir(), "b"))
	if a == b || a == "" {
		t.Fatalf("%q and %q", a, b)
	}
}

func TestDeviceOrderIsKeptAcrossRestartsAndMerges(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }
	self := ""
	client := &scriptedClient{handle: func(call scriptedCall) (int, any, string) {
		if strings.HasPrefix(call.Path, "/v1/sync/snapshot") {
			return 200, SyncSnapshotResponse{Seq: 1, Devices: []RemoteDevicePayload{
				{ID: "dev-a"}, {ID: "dev-b"}, {ID: self, IsCurrent: true}, {ID: "dev-new"},
			}}, ""
		}
		return accepting(call)
	}}
	home, secrets := t.TempDir(), &fakeSecrets{}
	mgr := newSignedIn(t, home, secrets, client, clock, oneMonitor(10))
	self = mgr.CurrentDevice()
	ctx := context.Background()
	order := func(m *Manager) string {
		if err := m.PullOnce(ctx); err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, device := range m.Devices() {
			ids = append(ids, device.ID)
		}
		return strings.Join(ids, " ")
	}
	if got := order(mgr); got != self+" dev-a dev-b dev-new" {
		t.Fatalf("this computer leads by default: %s", got)
	}
	if err := mgr.SetDeviceOrder([]string{"dev-b", self, "dev-a", "dev-b"}); err != nil {
		t.Fatal(err)
	}
	restarted := NewManager(Options{Home: home, Secrets: secrets, Client: client, Now: clock})
	if got := order(restarted); got != "dev-b "+self+" dev-a dev-new" {
		t.Fatalf("after a restart: %s", got)
	}
	if err := restarted.SetDeviceOrder([]string{"../x"}); err == nil {
		t.Fatal("an invalid ID must be refused")
	}
	if err := restarted.MergeDevice(ctx, "dev-a"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(restarted.state.DeviceOrder, " "); got != "dev-b dev-a" {
		t.Fatalf("the merged computer keeps its place: %s", got)
	}
}
