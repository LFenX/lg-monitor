//go:build integration && windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestPersonalHomeIsProtectedForItsOwnAccount(t *testing.T) {
	if !Personal() {
		t.Skip("personal homes apply only to non-elevated processes")
	}
	home := t.TempDir()
	child := filepath.Join(home, "data", "auth.db")
	if err := os.MkdirAll(filepath.Dir(child), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NativePathPolicy().ProtectTree(home, false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{home, child} {
		if err := NativePathPolicy().Check(path); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	descriptor, err := windows.GetNamedSecurityInfo(home, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !trustedWriter(sid) {
			t.Fatalf("unexpected grant to %s", sid.String())
		}
	}
	// Anything the account does not own, such as a service home, is never treated as personal.
	if OwnedByCurrentUser(os.Getenv("SystemRoot")) {
		t.Fatal("system directory treated as personal")
	}
}

// A service home's data directory cannot even be opened without elevation. ProtectTree must report that as a
// permission failure, because agent connect --elevate only asks Windows for approval on permission failures.
func TestPersonalProcessReportsUnreadableServiceHomeAsPermissionFailure(t *testing.T) {
	if !Personal() {
		t.Skip("only a non-elevated process is refused by an administrators-only home")
	}
	home := filepath.Join(t.TempDir(), "fake-service-home")
	data := filepath.Join(home, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	restrict := func(path, grants string) {
		t.Helper()
		descriptor, err := windows.SecurityDescriptorFromString(grants)
		if err != nil {
			t.Fatal(err)
		}
		dacl, _, err := descriptor.DACL()
		if err != nil {
			t.Fatal(err)
		}
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
			t.Fatal(err)
		}
	}
	// The test account stays owner, so it can hand access back for TempDir cleanup.
	t.Cleanup(func() {
		user := personalOwner().String()
		restrict(home, "D:P(A;OICI;FA;;;"+user+")")
		restrict(data, "D:P(A;OICI;FA;;;"+user+")")
	})
	restrict(data, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	restrict(home, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if _, err := os.Lstat(data); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("fixture is readable without elevation: %v", err)
	}
	err := NativePathPolicy().ProtectTree(data, false)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unreadable home was not reported as a permission failure: %v", err)
	}
}
