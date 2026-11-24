// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestLANWebVisitorSessionIsReadOnlyAndTracked(t *testing.T) {
	now := time.UnixMilli(2_000_000_000_000)
	policy, err := NewHostPolicy("0.0.0.0:7444", "", []string{"192.168.1.10"})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := NewAuthenticator(AuthOptions{
		Tokens:  TokenLookupFunc(func(context.Context, [32]byte) (Principal, bool, error) { return Principal{}, false, nil }),
		Entropy: rand.Reader,
		Hosts:   policy,
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.SetLocalScope(ScopeConfig); err != nil {
		t.Fatal(err)
	}
	// Disabled by default: LAN peer gets no guest session.
	lanReq := httptest.NewRequest(http.MethodGet, "http://192.168.1.10:7444/", nil)
	lanReq.RemoteAddr = "192.168.1.42:54321"
	if token, local, guest, err := auth.WebSession(lanReq); err != nil || token != "" || local || guest {
		t.Fatalf("disabled LAN web returned token=%q local=%v guest=%v err=%v", token, local, guest, err)
	}

	auth.SetLANWeb(true)
	guestToken, local, guest, err := auth.WebSession(lanReq)
	if err != nil || guestToken == "" || local || !guest {
		t.Fatalf("enabled LAN web returned token=%q local=%v guest=%v err=%v", guestToken, local, guest, err)
	}

	// Public IPs and proxied requests must never receive a guest session.
	pubReq := httptest.NewRequest(http.MethodGet, "http://192.168.1.10:7444/", nil)
	pubReq.RemoteAddr = "203.0.113.5:12345"
	if token, _, guest, err := auth.WebSession(pubReq); err != nil || token != "" || guest {
		t.Fatalf("public IP got guest session: token=%q guest=%v err=%v", token, guest, err)
	}
	proxyReq := httptest.NewRequest(http.MethodGet, "http://192.168.1.10:7444/", nil)
	proxyReq.RemoteAddr = "192.168.1.42:54321"
	proxyReq.Header.Set("X-Forwarded-For", "192.168.1.99")
	if token, _, guest, err := auth.WebSession(proxyReq); err != nil || token != "" || guest {
		t.Fatalf("proxied request got guest session: token=%q guest=%v err=%v", token, guest, err)
	}

	// Read route succeeds with X-Lgmon-Guest: 1 and strictly ScopeRead.
	readHandler, err := auth.Protect(ScopeRead, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, _ := CurrentPrincipal(r.Context())
		writeJSON(w, 200, principal)
	}))
	if err != nil {
		t.Fatal(err)
	}
	apiRead := httptest.NewRequest(http.MethodGet, "http://192.168.1.10:7444/api/v1/boards", nil)
	apiRead.RemoteAddr = "192.168.1.42:54322"
	apiRead.Header.Set("Authorization", "Bearer "+guestToken)
	rec := httptest.NewRecorder()
	readHandler.ServeHTTP(rec, apiRead)
	if rec.Code != 200 || rec.Header().Get("X-Lgmon-Scope") != "read" || rec.Header().Get("X-Lgmon-Guest") != "1" {
		t.Fatalf("guest read: code=%d scope=%q guest=%q body=%s", rec.Code, rec.Header().Get("X-Lgmon-Scope"), rec.Header().Get("X-Lgmon-Guest"), rec.Body.String())
	}
	var got Principal
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Scope != ScopeRead || !got.Guest || got.Local {
		t.Fatalf("guest principal=%+v err=%v", got, err)
	}

	// Config route must reject guest token with 403 even when localScope is config.
	cfgHandler, err := auth.Protect(ScopeConfig, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	if err != nil {
		t.Fatal(err)
	}
	apiWrite := httptest.NewRequest(http.MethodPut, "http://192.168.1.10:7444/api/v1/profile", nil)
	apiWrite.RemoteAddr = "192.168.1.42:54323"
	apiWrite.Header.Set("Authorization", "Bearer "+guestToken)
	recWrite := httptest.NewRecorder()
	cfgHandler.ServeHTTP(recWrite, apiWrite)
	if recWrite.Code != 403 {
		t.Fatalf("guest write must be forbidden, got %d %s", recWrite.Code, recWrite.Body.String())
	}

	// Visitor tracker lists active visitor with deterministic avatar, and expires after 90s idle.
	visitors := auth.Visitors()
	if len(visitors) != 1 || visitors[0].IP != "192.168.1.42" || visitors[0].Avatar == "" {
		t.Fatalf("visitors=%+v", visitors)
	}
	leave := auth.EnterGuestStream("192.168.1.42")
	now = now.Add(2 * time.Minute)
	if live := auth.Visitors(); len(live) != 1 || live[0].Connections != 1 {
		t.Fatalf("live SSE visitor should remain active: %+v", live)
	}
	leave()
	now = now.Add(2 * time.Minute)
	if expired := auth.Visitors(); len(expired) != 0 {
		t.Fatalf("idle visitor should expire: %+v", expired)
	}
}

func TestGitHubStatsCountsGitHubAssetsOnly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repo", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]int{"stargazers_count": 42, "forks_count": 7})
	})
	mux.HandleFunc("/releases", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, []map[string]any{
			{"tag_name": "v0.18.0", "assets": []map[string]any{
				{"name": "lgmon-setup-0.18.0.exe", "download_count": 100},
				{"name": "lgmon-linux-amd64.tar.gz", "download_count": 18},
				{"name": "lgmon-linux-arm64.tar.gz", "download_count": 7},
				{"name": "install.sh", "download_count": 12},
				{"name": "SHA256SUMS", "download_count": 25},
			}},
			{"tag_name": "v0.14.1", "assets": []map[string]any{{"name": "lgmon-0.1.0-windows-amd64-setup.exe", "download_count": 21}, {"name": "lgmon-0.1.0-windows-amd64-setup.json", "download_count": 5}, {"name": "other.zip", "download_count": 4}}},
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	now := time.UnixMilli(2_000_000_000_000)
	cache := &githubStatsCache{
		now:         func() time.Time { return now },
		client:      ts.Client(),
		repoURL:     ts.URL + "/repo",
		releasesURL: ts.URL + "/releases",
	}
	stats := cache.get(context.Background())
	if stats.Downloads != 192 || stats.Stars != 42 || stats.Forks != 7 || stats.Version != "v0.18.0" {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	want := AssetCounts{Installer: 100, Linux: 25, LinuxScript: 12, LegacyInstaller: 21, Checksums: 25, Metadata: 5, Other: 4}
	if stats.Assets == nil || *stats.Assets != want || stats.Releases[1].Assets.LegacyInstaller != 21 || stats.Releases[0].Assets.Linux != 25 {
		t.Fatalf("asset categories: %+v", stats)
	}

}

func TestGitHubStatsKeepLastCountsAndBackOffWhenQuotaIsSpent(t *testing.T) {
	now := time.UnixMilli(2_000_000_000_000)
	calls := 0
	limited := false
	mux := http.NewServeMux()
	mux.HandleFunc("/repo", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]int{"stargazers_count": 3, "forks_count": 1})
	})
	mux.HandleFunc("/releases", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if limited {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(now.Add(30*time.Minute).Unix(), 10))
			writeJSON(w, 403, map[string]string{"message": "API rate limit exceeded"})
			return
		}
		writeJSON(w, 200, []map[string]any{{"tag_name": "v0.17.3", "assets": []map[string]any{{"name": "lgmon-setup-0.17.3.exe", "download_count": 9}}}})
	})
	mux.HandleFunc("/latest", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://github.com/LFenX/lg-monitor/releases/tag/v0.18.0")
		w.WriteHeader(http.StatusFound)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	file := filepath.Join(t.TempDir(), "data", "github-stats.json")
	fresh := func(current string) *githubStatsCache {
		return &githubStatsCache{
			now: func() time.Time { return now }, client: ts.Client(), file: file, current: current,
			repoURL: ts.URL + "/repo", releasesURL: ts.URL + "/releases", pageURL: ts.URL + "/latest",
		}
	}

	cache := fresh("0.17.1")
	if stats := cache.get(context.Background()); !stats.Available || stats.Downloads != 9 || stats.Version != "v0.17.3" {
		t.Fatalf("first read: %+v", stats)
	}
	// A restart on a network whose quota is spent still shows the saved counts and does not retry each request.
	limited = true
	now = now.Add(time.Hour)
	restarted := fresh("0.17.1")
	for range 3 {
		if stats := restarted.get(context.Background()); !stats.Available || stats.Downloads != 9 || stats.Stars != 3 {
			t.Fatalf("saved counts: %+v", stats)
		}
	}
	if calls != 2 {
		t.Fatalf("GitHub was asked %d times, want 2", calls)
	}
	now = now.Add(29 * time.Minute)
	restarted.get(context.Background())
	if calls != 2 {
		t.Fatalf("retried before the quota reset: %d calls", calls)
	}

	// With nothing saved, the bar hides counts but still names the latest version from the public page.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	empty := fresh("0.17.1")
	if stats := empty.get(context.Background()); stats.Available || stats.Version != "v0.18.0" {
		t.Fatalf("no saved counts: %+v", stats)
	}
	offline := fresh("0.17.1")
	offline.pageURL = ts.URL + "/missing"
	if stats := offline.get(context.Background()); stats.Available || stats.Version != "v0.17.1" {
		t.Fatalf("running version fallback: %+v", stats)
	}
}

func TestLatestDownloadRoutesWindowsAndLinuxChannels(t *testing.T) {
	s := serverFixture(t, nil)
	now := time.UnixMilli(2_000_000_000_000)
	s.downloads.now = func() time.Time { return now }
	s.downloads.seed(githubRepoPage+"/releases/download/v0.18.0/lgmon-setup-0.18.0.exe", now.Add(time.Hour))

	cases := []struct {
		name   string
		target string
		ua     string
		want   string
	}{
		{
			name:   "default windows installer",
			target: "/api/v1/download/latest",
			ua:     "Mozilla/5.0 (Windows NT 10.0; Win64; x64)",
			want:   githubRepoPage + "/releases/download/v0.18.0/lgmon-setup-0.18.0.exe",
		},
		{
			name:   "windows guest channel",
			target: "/api/v1/download/latest?channel=guest",
			ua:     "Mozilla/5.0 (Windows NT 10.0; Win64; x64)",
			want:   "https://license.lfen.ink/download/guest/v0.18.0",
		},
		{
			name:   "linux explicit arm64",
			target: "/api/v1/download/latest?os=linux&arch=arm64",
			ua:     "Mozilla/5.0 (Windows NT 10.0; Win64; x64)",
			want:   githubRepoPage + "/releases/download/v0.18.0/lgmon-linux-arm64.tar.gz",
		},
		{
			name:   "linux install script",
			target: "/api/v1/download/latest?os=linux&kind=script",
			want:   githubRepoPage + "/releases/download/v0.18.0/install.sh",
		},
		{
			name:   "linux detected from user agent",
			target: "/api/v1/download/latest",
			ua:     "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36",
			want:   githubRepoPage + "/releases/download/v0.18.0/lgmon-linux-amd64.tar.gz",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7444"+tc.target, nil)
			req.RemoteAddr = "127.0.0.1:1234"
			if tc.ua != "" {
				req.Header.Set("User-Agent", tc.ua)
			}
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, req)
			if rec.Code != http.StatusFound {
				t.Fatalf("status=%d want 302 body=%s", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Location"); got != tc.want {
				t.Fatalf("Location=%q want %q", got, tc.want)
			}
		})
	}
}
