//go:build integration && windows

package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func setTestPermissions(t *testing.T, path, grants string) {
	t.Helper()
	descriptor, err := windows.SecurityDescriptorFromString("O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)" + grants)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, owner, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPathPolicyRejectsUntrustedWriters(t *testing.T) {
	for _, tc := range []struct {
		name, grants string
		allowed      bool
	}{
		{"users write", "(A;OICI;GW;;;BU)", false},
		{"authenticated modify", "(A;OICI;0x1301bf;;;AU)", false},
		{"users delete child", "(A;OICI;0x40;;;BU)", false},
		{"users change permissions", "(A;OICI;WD;;;BU)", false},
		{"admins only", "", true},
		{"users read execute", "(A;OICI;GRGX;;;BU)", true},
		{"trusted installer", "(A;OICI;FA;;;" + trustedInstallerSID + ")", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := t.TempDir()
			setTestPermissions(t, path, tc.grants)
			if err := NativePathPolicy().Check(path); (err == nil) != tc.allowed {
				t.Fatalf("permission decision: %v", err)
			}
		})
	}
}

func TestProtectedHomeClearsExistingGrantsAndChildrenInherit(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, "config")
	if err := os.Mkdir(old, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(old, "own.yaml")
	if err := os.WriteFile(file, []byte("fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	setTestPermissions(t, old, "(A;OICI;FA;;;BU)")
	setTestPermissions(t, file, "(A;;FA;;;AU)")
	if err := NativePathPolicy().ProtectTree(home, false); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(home, "new-child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	newFile := filepath.Join(child, "own.txt")
	if err := os.WriteFile(newFile, []byte("fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{home, old, file, child, newFile} {
		descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		control, _, err := descriptor.Control()
		if err != nil || (control&windows.SE_DACL_PROTECTED != 0) != (path == home) {
			t.Fatalf("inheritance protection: %s %v", path, err)
		}
		dacl, _, err := descriptor.DACL()
		if err != nil || dacl == nil || dacl.AceCount != 2 {
			t.Fatalf("unexpected home ACL: %s %v", path, err)
		}
		for i := uint32(0); i < uint32(dacl.AceCount); i++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(dacl, i, &ace); err != nil {
				t.Fatal(err)
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !sid.IsWellKnown(windows.WinLocalSystemSid) && !sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
				t.Fatalf("ordinary principal retained: %s", sid.String())
			}
			if path != home && ace.Header.AceFlags&windows.INHERITED_ACE == 0 {
				t.Fatalf("child did not inherit: %s", path)
			}
		}
	}
	if err := NativePathPolicy().Check(file); err != nil {
		t.Fatal(err)
	}
}

func TestProtectedTreeRejectsHardlinksWithoutChangingTarget(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	target := filepath.Join(other, "target.txt")
	if err := os.WriteFile(target, []byte("fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	before, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if err := NativePathPolicy().ProtectTree(root, false); err == nil || !strings.Contains(err.Error(), "硬链接") {
		t.Fatalf("hardlink accepted: %v", err)
	}
	after, err := windows.GetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil || before.String() != after.String() {
		t.Fatalf("hardlink target ACL changed: %v", err)
	}
}
