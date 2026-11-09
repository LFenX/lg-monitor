//go:build integration && windows

package transport

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/LFenX/lg-monitor/internal/config"
	"golang.org/x/sys/windows"
)

func TestNativeTargetCacheRestoresVerifiedSnapshotAndKeepsProtectedOwner(t *testing.T) {
	ctx := context.Background()
	files := FileTargetCache{Home: t.TempDir()}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cache, err := NewCachedTargets(ctx, files, logger)
	if err != nil {
		t.Fatal(err)
	}
	targets := testTargetConfig("local-cache")
	targets.Targets[0].Match.Cmdline = `--serve "<own-cache>"`
	for _, id := range []string{"local-cache", "updated-cache"} {
		targets.Targets[0].ID = id
		version, data, _ := config.TargetConfig(targets, "fake-collector", "")
		if err := cache.Apply(ctx, version, data); err != nil {
			t.Fatal(err)
		}
		loadedVersion, loadedData, err := files.Load(ctx)
		if err != nil || version != loadedVersion || !bytes.Equal(data, loadedData) {
			t.Fatalf("cache altered content or version: %v", err)
		}
		restarted, err := NewCachedTargets(ctx, files, logger)
		if err != nil || restarted.Snapshot().Targets[0].ID != id {
			t.Fatalf("offline cache recovery: %v", err)
		}
	}
	path, err := files.path()
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Dir(path)
	descriptor, err := windows.GetNamedSecurityInfo(directory, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("cache ACL is not protected: %v", err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || !windows.EqualSid(owner, user.User.Sid) {
		t.Fatalf("cache owner changed: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "targets.json" {
		t.Fatalf("temporary cache files leaked: %v %v", entries, err)
	}
}
