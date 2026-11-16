package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/update"
)

// recheck is how often an open window looks for a new version again.
const recheck = 6 * time.Hour

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
		"version": u.release.Version, "notes": u.release.Notes, "size": u.release.Size, "done": u.done, "total": u.total,
	}
}

func (u *updater) set(change func()) {
	u.mu.Lock()
	change()
	snapshot := u.snapshot()
	u.mu.Unlock()
	if u.notify != nil {
		u.notify(snapshot)
	}
}

// check looks for a newer release, at most every recheck unless forced; a development build never does.
func (u *updater) check(ctx context.Context, force bool) {
	u.mu.Lock()
	if u.current == "" || u.current == "dev" {
		u.mu.Unlock()
		u.set(func() { u.state = "dev" })
		return
	}
	recent := !force && u.state != "" && u.state != "error" && time.Since(u.checked) < recheck
	busy := u.state == "checking" || u.state == "downloading"
	u.mu.Unlock()
	if recent || busy {
		u.set(func() {})
		return
	}
	u.set(func() { u.state, u.message = "checking", "" })
	release, err := update.Latest(ctx, u.client, u.feed)
	u.set(func() {
		u.checked = time.Now()
		switch {
		case err != nil:
			u.state, u.during, u.message = "error", "check", err.Error()
		case release.Version == "" || !update.Newer(release.Version, u.current):
			u.state = "none"
		default:
			u.release, u.state, u.during = release, "available", ""
		}
	})
}

// download fetches the installer and checks it; afterwards the page offers to restart and update.
func (u *updater) download(ctx context.Context) {
	u.mu.Lock()
	if u.state != "available" && !(u.state == "error" && u.during == "download") {
		u.mu.Unlock()
		return
	}
	release := u.release
	u.mu.Unlock()
	u.set(func() { u.state, u.done, u.total, u.message = "downloading", 0, release.Size, "" })
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
