//go:build integration && windows

package secrets

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestMachineDPAPIAndRestrictedFileACL(t *testing.T) {
	home := t.TempDir()
	store, err := New(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	const plain = "fake-DPAPI-integration-value"
	if err := store.Set(context.Background(), "test-app", []byte(plain)); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), "test-app")
	if err != nil || string(got) != plain {
		t.Fatalf("DPAPI round trip failed: %v", err)
	}
	clear(got)
	path := filepath.Join(home, "data", "secrets.json")
	data, err := os.ReadFile(path)
	if err != nil || bytes.Contains(data, []byte(plain)) {
		t.Fatal("plaintext was persisted")
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("secret file still inherits permissions")
	}
	owner, err := (filePolicy{}).CurrentUser()
	if err != nil {
		t.Fatal(err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{system.String(): true, administrators.String(): true, owner: true}
	seen := make(map[string]bool)
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 3 {
		t.Fatal("secret file does not have exactly three explicit access entries")
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !allowed[sid] {
			t.Fatal("secret ACL grants access outside the allowed principals")
		}
		seen[sid] = true
	}
	for sid := range allowed {
		if !seen[sid] {
			t.Fatal("required principal is absent from secret ACL")
		}
	}
	if err := store.Set(context.Background(), "test-app", []byte("fake-replacement-value")); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(context.Background(), "test-app")
	if err != nil || string(got) != "fake-replacement-value" {
		t.Fatalf("encrypted file replacement failed: %v", err)
	}
	clear(got)
}
