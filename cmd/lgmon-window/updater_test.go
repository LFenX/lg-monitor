package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
