// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/update"
)

// recheck is how often an open window looks for a new version again.
const recheck = 20 * time.Minute

// updater is what the window knows about a newer lgmon, and it does the downloading for the page. Every change
// is sent to the page in full, so the page never has to piece the state together.
type updater struct {
	mu      sync.Mutex
	current string
	feed    string
	dir     string
	client  *http.Client
	notify  func(map[string]any)
	state   string
	during  string
	message string
	release update.Release
	checked time.Time
	path    string
	done    int64
	total   int64
}

func (u *updater) snapshot() map[string]any {
	return map[string]any{
		"type": "update", "state": u.state, "during": u.during, "message": u.message, "current": u.current,
		"version": u.release.Version, "notes": u.release.Notes, "hasPage": u.release.Page != "",
		"size": u.release.Size, "done": u.done, "total": u.total,
	}
}

func (u *updater) set(change func()) {
	u.mu.Lock()
	defer u.mu.Unlock()
	change()
	if u.notify != nil {
		u.notify(u.snapshot())
	}
}

// check looks for a newer release, at most every recheck unless forced; a development build never does.
func (u *updater) check(ctx context.Context, force bool) {
	start := false
	u.set(func() {
		if u.current == "" || u.current == "dev" {
			u.state = "dev"
			return
		}
		recent := !force && u.state != "" && u.state != "error" && time.Since(u.checked) < recheck
		if recent || u.state == "checking" || u.state == "downloading" || u.state == "ready" {
			return
		}
		u.state, u.during, u.message = "checking", "", ""
		start = true
	})
	if !start {
		return
	}
	release, err := update.Latest(ctx, u.client, u.feed)
	u.set(func() {
		u.checked = time.Now()
		u.release = update.Release{}
		switch {
		case err != nil:
			u.state, u.during, u.message = "error", "check", err.Error()
		case release.Version == "" || !update.Newer(release.Version, u.current):
			u.state = "none"
		default:
			u.release, u.state = release, "available"
		}
	})
}

// download fetches the installer and checks it; afterwards the page offers to restart and update.
func (u *updater) download(ctx context.Context) {
	var release update.Release
	u.set(func() {
		if u.state == "available" || (u.state == "error" && u.during == "download") {
			release = u.release
			u.state, u.done, u.total, u.message = "downloading", 0, release.Size, ""
		}
	})
	if release.Version == "" {
		return
	}
	path, err := update.Download(ctx, u.client, release, u.dir, func(done, total int64) {
		u.set(func() { u.done, u.total = done, total })
	})
	u.set(func() {
		if err != nil {
			u.state, u.during, u.message = "error", "download", err.Error()
			return
		}
		u.path, u.state = path, "ready"
	})
}

// ready is the downloaded installer, if there is one.
func (u *updater) ready() (string, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.path, u.state == "ready" && u.path != ""
}

func (u *updater) page() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.release.Page
}
