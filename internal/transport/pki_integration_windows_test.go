//go:build integration && windows

package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"github.com/LFenX/lg-monitor/internal/secrets"
	"golang.org/x/sys/windows"
)

func TestFileStatePublishesWholeDocumentWithoutOverwrite(t *testing.T) {
	ctx := context.Background()
	state := FileState{Home: t.TempDir()}
	if _, err := state.Load(ctx, "authority"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing state: %v", err)
	}
	if err := state.Save(ctx, "authority", []byte(`{"version":1}`), false); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(ctx, "authority", []byte(`{"version":2}`), false); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing state overwrite: %v", err)
	}
	data, err := state.Load(ctx, "authority")
	if err != nil || string(data) != `{"version":1}` {
		t.Fatalf("initial state: %s %v", data, err)
	}
	if err := state.Save(ctx, "authority", []byte(`{"version":2}`), true); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(ctx, "../outside", nil, true); err == nil {
		t.Fatal("state path escaped fixed names")
	}
	entries, err := os.ReadDir(filepath.Join(state.Home, "data", "pki"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary file leak: %v %v", entries, err)
	}
}

func TestNativePKIEncryptsKeysAndRestrictsTrustDirectory(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	keys, err := secrets.New(home, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	state := FileState{Home: home}
	authority, err := NewAuthority(ctx, AuthorityOptions{Keys: keys, State: state, SANs: &fakeSANs{}, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := NewAuthority(ctx, AuthorityOptions{Keys: keys, State: state, SANs: &fakeSANs{}, Now: time.Now})
	if err != nil || loaded.Fingerprint() != authority.Fingerprint() {
		t.Fatalf("native reload: %v", err)
	}
	public, err := state.Load(ctx, "authority")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := os.ReadFile(filepath.Join(home, "data", "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	names, err := keys.List(ctx)
	if err != nil || len(names) != 3 {
		t.Fatalf("key references: %v", err)
	}
	for _, name := range names {
		plain, err := keys.Get(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(public, plain) || bytes.Contains(encrypted, plain) {
			clear(plain)
			t.Fatal("private key persisted without encryption")
		}
		clear(plain)
	}
	path := filepath.Join(home, "data", "pki")
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("trust directory inherits external permissions")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil || !owner.Equals(user.User.Sid) {
		t.Fatal("initial directory owner is not the creating user")
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{system.String(): true, admins.String(): true, user.User.Sid.String(): true}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 3 {
		t.Fatal("trust directory must have exactly three explicit grants")
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !allowed[sid] || ace.Header.AceFlags&windows.OBJECT_INHERIT_ACE == 0 || ace.Header.AceFlags&windows.CONTAINER_INHERIT_ACE == 0 {
			t.Fatal("trust directory grants an unexpected principal or lacks inheritance")
		}
	}
	if err := state.Save(ctx, "endpoint", []byte(`{"address":"127.0.0.1:7443"}`), true); err != nil {
		t.Fatal(err)
	}
	updated, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	newOwner, _, err := updated.Owner()
	if err != nil || !newOwner.Equals(owner) {
		t.Fatal("subsequent state update changed directory owner")
	}
}
