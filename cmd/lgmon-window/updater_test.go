// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func fakeFeed(t *testing.T, installer []byte) *httptest.Server {
	t.Helper()
	digest := sha256.Sum256(installer)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.9.0", "body": "虚构更新", "html_url": base + "/page",
				"assets": []map[string]any{
					{"name": "lgmon-setup-0.9.0.exe", "browser_download_url": base + "/setup", "size": len(installer)},
					{"name": "SHA256SUMS", "browser_download_url": base + "/sums"},
				}})
		case "/sums":
			w.Write([]byte(hex.EncodeToString(digest[:]) + "  lgmon-setup-0.9.0.exe\n"))
		case "/setup":
			w.Write(installer)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestManualCheckRefreshesARecentResult(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	u := &updater{current: "0.12.0", feed: server.URL, client: server.Client()}
	u.check(context.Background(), false)
	u.check(context.Background(), false)
	if calls.Load() != 1 || u.snapshot()["state"] != "none" {
		t.Fatalf("automatic checks: requests=%d state=%v", calls.Load(), u.snapshot()["state"])
	}
	u.check(context.Background(), true)
	if calls.Load() != 2 || u.snapshot()["state"] != "none" {
		t.Fatalf("manual check: requests=%d state=%v", calls.Load(), u.snapshot()["state"])
	}
}

func TestTheWindowFindsDownloadsAndOffersANewerRelease(t *testing.T) {
	server := fakeFeed(t, []byte("FAKE_SETUP"))
	var mu sync.Mutex
	var states []string
	record := func(state map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		states = append(states, state["state"].(string))
	}
	// A development build never asks.
	dev := &updater{current: "dev", feed: server.URL + "/latest", dir: t.TempDir(), client: server.Client(), notify: record}
	dev.check(context.Background(), true)
	if states[len(states)-1] != "dev" {
		t.Fatalf("dev states %v", states)
	}
	older := &updater{current: "0.8.0", feed: server.URL + "/latest", dir: t.TempDir(), client: server.Client(), notify: record}
	older.check(context.Background(), false)
	if snapshot := older.snapshot(); snapshot["state"] != "available" || snapshot["version"] != "0.9.0" || snapshot["notes"] != "虚构更新" {
		t.Fatalf("snapshot %v", snapshot)
	}
	if _, ok := older.ready(); ok {
		t.Fatal("ready before downloading")
	}
	older.download(context.Background())
	if path, ok := older.ready(); !ok || path == "" || older.snapshot()["done"] != int64(len("FAKE_SETUP")) {
		t.Fatalf("not ready: %v", older.snapshot())
	}
	if older.page() != server.URL+"/page" {
		t.Fatalf("notes page %q", older.page())
	}
	// The newest version has nothing to offer.
	newest := &updater{current: "0.9.0", feed: server.URL + "/latest", dir: t.TempDir(), client: server.Client(), notify: record}
	newest.check(context.Background(), false)
	if newest.snapshot()["state"] != "none" {
		t.Fatalf("newest %v", newest.snapshot())
	}
	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{"checking", "available", "downloading", "ready", "none"} {
		found := false
		for _, state := range states {
			found = found || state == want
		}
		if !found {
			t.Fatalf("never reported %s: %v", want, states)
		}
	}
}

func TestWebDownloadDuringNativeCheckSharesTheVerifiedInstaller(t *testing.T) {
	server := fakeFeed(t, []byte("FAKE_SETUP"))
	started, resume := make(chan struct{}), make(chan struct{})
	waiting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-resume
		http.Redirect(w, r, server.URL+"/latest", http.StatusFound)
	}))
	defer waiting.Close()
	u := &updater{current: "0.8.0", feed: waiting.URL, dir: t.TempDir(), client: server.Client()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		u.check(context.Background(), false)
	}()
	<-started
	u.checkForUpdate(context.Background(), true, true, false)
	close(resume)
	<-done
	path, ok := u.ready()
	if !ok {
		t.Fatalf("download requested during check was lost: %v", u.snapshot())
	}
	u.checkForUpdate(context.Background(), true, true, false)
	if again, ok := u.ready(); !ok || again != path {
		t.Fatalf("duplicate request lost the verified installer: %v", u.snapshot())
	}
	cold := &updater{current: "0.8.0", feed: server.URL + "/latest", dir: t.TempDir(), client: server.Client()}
	cold.checkForUpdate(context.Background(), true, true, false)
	if _, ok := cold.ready(); !ok {
		t.Fatalf("cold window failed to download: %v", cold.snapshot())
	}
	// Auto-apply completes the full flow (check -> download -> verify -> apply callback).
	var applied string
	auto := &updater{
		current: "0.8.0",
		feed:    server.URL + "/latest",
		dir:     t.TempDir(),
		client:  server.Client(),
		onApply: func(p string) { applied = p },
	}
	auto.checkForUpdate(context.Background(), true, true, true)
	if applied == "" || auto.snapshot()["state"] != "applying" {
		t.Fatalf("auto-apply did not run installer: applied=%q snap=%v", applied, auto.snapshot())
	}
}

// releases is a feed whose latest version can move on while a window is open.
type releases struct {
	*httptest.Server
	version atomic.Value
	hold    atomic.Value // a version whose download never finishes
	held    chan struct{}
}

func movingFeed(t *testing.T, version string) *releases {
	t.Helper()
	feed := &releases{held: make(chan struct{}, 4)}
	feed.version.Store(version)
	feed.hold.Store("")
	body := func(version string) []byte { return []byte("FAKE_SETUP_" + version) }
	feed.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base, current := "http://"+r.Host, feed.version.Load().(string)
		switch {
		case r.URL.Path == "/latest" && current == "":
			w.WriteHeader(http.StatusBadGateway)
		case r.URL.Path == "/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v" + current, "html_url": base + "/page",
				"assets": []map[string]any{
					{"name": "lgmon-setup-" + current + ".exe", "browser_download_url": base + "/setup/" + current, "size": len(body(current))},
					{"name": "SHA256SUMS", "browser_download_url": base + "/sums/" + current},
				}})
		case strings.HasPrefix(r.URL.Path, "/sums/"):
			version := strings.TrimPrefix(r.URL.Path, "/sums/")
			digest := sha256.Sum256(body(version))
			_, _ = w.Write([]byte(hex.EncodeToString(digest[:]) + "  lgmon-setup-" + version + ".exe\n"))
		case strings.HasPrefix(r.URL.Path, "/setup/"):
			version := strings.TrimPrefix(r.URL.Path, "/setup/")
			if feed.hold.Load() == version {
				_, _ = w.Write([]byte("F"))
				w.(http.Flusher).Flush()
				feed.held <- struct{}{}
				<-r.Context().Done()
				return
			}
			_, _ = w.Write(body(version))
		}
	}))
	t.Cleanup(feed.Close)
	return feed
}

func (r *releases) updater(t *testing.T) *updater {
	return &updater{current: "0.8.0", feed: r.URL + "/latest", dir: t.TempDir(), client: r.Client()}
}

func TestAnOfferOvertakenByANewerReleaseMovesOn(t *testing.T) {
	ctx := context.Background()
	feed := movingFeed(t, "0.9.0")
	u := feed.updater(t)
	u.check(ctx, false)
	feed.version.Store("0.10.0")
	u.check(ctx, false)
	if got := u.snapshot()["version"]; got != "0.9.0" {
		t.Fatalf("a recent offer should not be looked up again at once: %v", got)
	}
	u.check(ctx, true)
	if got := u.snapshot(); got["version"] != "0.10.0" || got["state"] != "available" {
		t.Fatalf("offer %v", got)
	}
	// Downloading an offer that has been overtaken gets the newest one straight away.
	feed.version.Store("0.11.0")
	u.download(ctx)
	if path, ok := u.ready(); !ok || !strings.HasSuffix(path, "lgmon-setup-0.11.0.exe") {
		t.Fatalf("downloaded %q, %v", path, u.snapshot())
	}
	// Without an answer the downloaded one stays on offer.
	feed.version.Store("")
	u.check(ctx, true)
	if path, ok := u.ready(); !ok || !strings.HasSuffix(path, "lgmon-setup-0.11.0.exe") {
		t.Fatalf("an unreachable feed lost the download: %q, %v", path, u.snapshot())
	}
}

func TestADownloadedVersionIsReplacedByTheNewestOne(t *testing.T) {
	ctx := context.Background()
	feed := movingFeed(t, "0.9.0")
	// An open window that has an older version waiting fetches the newer one by itself.
	waiting := feed.updater(t)
	waiting.checkForUpdate(ctx, true, true, false)
	feed.version.Store("0.10.0")
	waiting.check(ctx, true)
	if path, ok := waiting.ready(); !ok || !strings.HasSuffix(path, "lgmon-setup-0.10.0.exe") {
		t.Fatalf("waiting window has %q, %v", path, waiting.snapshot())
	}
	// "重启并更新" installs the newest version even if nothing has looked since the download.
	feed.version.Store("0.11.0")
	var applied []string
	waiting.onApply = func(path string) { applied = append(applied, path) }
	waiting.apply(ctx)
	if len(applied) != 1 || !strings.HasSuffix(applied[0], "lgmon-setup-0.11.0.exe") || waiting.snapshot()["state"] != "applying" {
		t.Fatalf("applied %v, %v", applied, waiting.snapshot())
	}
	// With nothing newer it simply runs what it has, also when the feed cannot be reached.
	for _, latest := range []string{"0.11.0", ""} {
		feed.version.Store("0.11.0")
		ready := feed.updater(t)
		ready.checkForUpdate(ctx, true, true, false)
		feed.version.Store(latest)
		var ran string
		ready.onApply = func(path string) { ran = path }
		ready.checkForUpdate(ctx, true, false, true)
		if !strings.HasSuffix(ran, "lgmon-setup-0.11.0.exe") {
			t.Fatalf("feed %q: ran %q, %v", latest, ran, ready.snapshot())
		}
	}
}

func TestAStuckDownloadGivesWayToANewerRelease(t *testing.T) {
	ctx := context.Background()
	feed := movingFeed(t, "0.9.0")
	feed.hold.Store("0.9.0")
	u := feed.updater(t)
	var applied string
	u.onApply = func(path string) { applied = path }
	done := make(chan struct{})
	go func() {
		defer close(done)
		u.checkForUpdate(ctx, true, true, true)
	}()
	<-feed.held
	feed.version.Store("0.10.0")
	u.check(ctx, true)
	<-done
	if !strings.HasSuffix(applied, "lgmon-setup-0.10.0.exe") || u.snapshot()["state"] != "applying" || u.snapshot()["version"] != "0.10.0" {
		t.Fatalf("applied %q, %v", applied, u.snapshot())
	}
}

func TestAFailedUpdateRetriesWithTheNewestVersion(t *testing.T) {
	ctx := context.Background()
	feed := movingFeed(t, "0.9.0")
	u := feed.updater(t)
	u.checkForUpdate(ctx, true, true, false)
	u.set(func() { u.state, u.during, u.message = "error", "apply", "fake failure" })
	feed.version.Store("0.10.0")
	u.download(ctx)
	if path, ok := u.ready(); !ok || !strings.HasSuffix(path, "lgmon-setup-0.10.0.exe") {
		t.Fatalf("retry got %q, %v", path, u.snapshot())
	}
}
