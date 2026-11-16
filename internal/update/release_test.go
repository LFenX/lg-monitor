package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeReleases struct {
	installer []byte
	sum       string
	missing   bool
}

func (f *fakeReleases) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	base := "http://" + r.Host
	switch r.URL.Path {
	case "/releases/latest":
		if f.missing {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v0.9.0", "body": "- 虚构更新内容", "html_url": base + "/page", "published_at": "2026-11-17T00:00:00Z",
			"assets": []map[string]any{
				{"name": "lgmon-0.9.0-windows-amd64-setup.exe", "browser_download_url": base + "/engine", "size": 9},
				{"name": "lgmon-setup-0.9.0.exe", "browser_download_url": base + "/setup", "size": len(f.installer)},
				{"name": "SHA256SUMS", "browser_download_url": base + "/sums", "size": 100},
			},
		})
	case "/sums":
		w.Write([]byte(strings.Repeat("0", 64) + "  lgmon-0.9.0-windows-amd64-setup.exe\n" + f.sum + "  lgmon-setup-0.9.0.exe\n"))
	case "/setup":
		w.Write(f.installer)
	default:
		http.NotFound(w, r)
	}
}

func TestLatestReleaseAndADownloadThatMustMatchItsChecksum(t *testing.T) {
	installer := []byte("FAKE_LGMON_SETUP_BYTES")
	digest := sha256.Sum256(installer)
	fake := &fakeReleases{installer: installer, sum: hex.EncodeToString(digest[:])}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	release, err := Latest(context.Background(), server.Client(), server.URL+"/releases/latest")
	if err != nil || release.Version != "0.9.0" || release.File != "lgmon-setup-0.9.0.exe" || release.SHA256 != fake.sum || !strings.Contains(release.Notes, "虚构") {
		t.Fatalf("release=%+v err=%v", release, err)
	}
	dir := t.TempDir()
	var seen int64
	path, err := Download(context.Background(), server.Client(), release, dir, func(done, total int64) { seen = done })
	if err != nil || filepath.Base(path) != release.File || seen != int64(len(installer)) {
		t.Fatalf("path=%s seen=%d err=%v", path, seen, err)
	}
	if data, _ := os.ReadFile(path); string(data) != string(installer) {
		t.Fatal("downloaded bytes differ")
	}
	// A second download reuses the file that already matches.
	fake.installer = []byte("CHANGED")
	if again, err := Download(context.Background(), server.Client(), release, dir, nil); err != nil || again != path {
		t.Fatalf("reuse=%s err=%v", again, err)
	}
	// A file that does not match the published checksum is refused and removed.
	os.Remove(path)
	if _, err := Download(context.Background(), server.Client(), release, dir, nil); err == nil || !strings.Contains(err.Error(), "校验") {
		t.Fatalf("mismatch err=%v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left behind %v", entries)
	}
	// No published release yet is not an error.
	fake.missing = true
	if none, err := Latest(context.Background(), server.Client(), server.URL+"/releases/latest"); err != nil || none.Version != "" {
		t.Fatalf("none=%+v err=%v", none, err)
	}
}
