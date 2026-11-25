// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

type releaseRoundTripper func(*http.Request) (*http.Response, error)

func (transport releaseRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestDefaultFeedUsesTheOfficialReleasePage(t *testing.T) {
	const tag = "v0.12.0"
	const file = "lgmon-setup-0.12.0.exe"
	const latestPage = "https://github.com/LFenX/lg-monitor/releases/latest"
	page := githubReleases + "/tag/" + tag
	assets := githubReleases + "/download/" + tag + "/"
	sum := strings.Repeat("1", 64)
	for _, tc := range []struct {
		name      string
		location  string
		checksum  string
		wantError string
		wantSums  bool
	}{
		{name: "stable release", location: page, checksum: sum, wantSums: true},
		{name: "relative link", location: "/LFenX/lg-monitor/releases/tag/" + tag, checksum: sum, wantSums: true},
		{name: "other repository", location: "https://github.com/someone/else/releases/tag/" + tag, wantError: "有效的正式版本"},
		{name: "prerelease", location: page + "-beta.1", wantError: "有效的正式版本"},
		{name: "missing installer checksum", location: page, checksum: "invalid", wantError: file, wantSums: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sawSums := false
			client := &http.Client{Transport: releaseRoundTripper(func(request *http.Request) (*http.Response, error) {
				recorder := httptest.NewRecorder()
				switch request.URL.String() {
				case latestPage:
					recorder.Header().Set("Location", tc.location)
					recorder.WriteHeader(http.StatusFound)
				case assets + "SHA256SUMS":
					sawSums = true
					recorder.WriteString(tc.checksum + "  " + file + "\n")
				default:
					t.Fatalf("unexpected request to %s", request.URL)
				}
				result := recorder.Result()
				result.Request = request
				return result, nil
			})}
			release, err := Latest(context.Background(), client, DefaultFeed)
			if sawSums != tc.wantSums {
				t.Fatalf("requested checksum: %v, want %v", sawSums, tc.wantSums)
			}
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || release != (Release{}) {
					t.Fatalf("release=%+v err=%v, want %q", release, err, tc.wantError)
				}
				return
			}
			if err != nil || release.Version != "0.12.0" || release.Page != page || release.File != file || release.URL != assets+file || release.SHA256 != sum {
				t.Fatalf("release=%+v err=%v", release, err)
			}
		})
	}
}

func TestADownloadWithoutProgressIsGivenUp(t *testing.T) {
	previous := stallTimeout
	stallTimeout = 100 * time.Millisecond
	defer func() { stallTimeout = previous }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("F"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	dir := t.TempDir()
	release := Release{Version: "0.9.0", File: "lgmon-setup-0.9.0.exe", URL: server.URL, SHA256: strings.Repeat("0", 64)}
	if _, err := Download(context.Background(), server.Client(), release, dir, nil); !errors.Is(err, errStalled) {
		t.Fatalf("expected a stalled download, got %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left behind %v", entries)
	}
}
