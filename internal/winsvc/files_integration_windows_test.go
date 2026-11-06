//go:build integration && windows

package winsvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/store"
	"golang.org/x/sys/windows"
)

func TestInstallFilesOnlyCopiesAndProtectsOwnTemporaryPaths(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.exe")
	if err := os.WriteFile(source, []byte("own test program"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := nativeFiles{policy: secrets.NativePathPolicy(), directory: filepath.Join(root, "Program Files", "lg-monitor")}
	definition, err := files.Prepare(context.Background(), Definition{Executable: source, Home: filepath.Join(root, "home")})
	if err != nil {
		t.Fatal(err)
	}
	if definition.Executable != filepath.Join(files.directory, "lgmon.exe") {
		t.Fatalf("wrong installed executable: %s", definition.Executable)
	}
	for _, path := range []string{source, definition.Executable} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "own test program" {
			t.Fatalf("program copy changed: %s %v", path, err)
		}
	}
	if err := prepareServicePaths(files.policy, definition.Executable, definition.Home); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Prepare(context.Background(), definition); err != nil {
		t.Fatalf("installation from protected copy failed: %v", err)
	}
}

func TestReadOnlyDataPermissionErrorRequestsAdministrator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "own.db")
	if err := os.WriteFile(path, []byte("own fake data"), 0o600); err != nil {
		t.Fatal(err)
	}
	original, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	originalACL, _, err := original.DACL()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString("O:BAD:P(D;;FR;;;WD)(A;;FA;;;SY)(A;;FA;;;BA)")
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
	defer func() {
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, originalACL, nil); err != nil {
			t.Error(err)
		}
	}()
	if view, err := store.OpenView(context.Background(), path); err == nil {
		view.Close()
		t.Fatal("read-denied data unexpectedly opened")
	} else if !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), "管理员身份") {
		t.Fatalf("permission hint lost: %v", err)
	}
}
