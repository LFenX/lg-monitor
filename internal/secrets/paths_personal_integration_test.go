//go:build integration && windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package secrets

import (
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
