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
	mu                 sync.Mutex
	current            string
	feed               string
	dir                string
	client             *http.Client
	notify             func(map[string]any)
	state              string
	during             string
	message            string
	release            update.Release
	checked            time.Time
	downloadAfterCheck bool
	applyAfterDownload bool
	onApply            func(path string)
	path               string
	// attempt tells a download whether a newer release has taken over from it; cancel stops it.
	attempt int
	cancel  context.CancelFunc
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
	u.checkForUpdate(ctx, force, false, false)
}

func (u *updater) checkForUpdate(ctx context.Context, force, download, autoApply bool) {
	if autoApply {
		download = true
	}
	var start, downloadNow, applyNow, refresh bool
	u.set(func() {
		if u.current == "" || u.current == "dev" {
			u.state = "dev"
			return
		}
		recent := !force && u.state != "" && u.state != "error" && time.Since(u.checked) < recheck
		failed := u.state == "error" && (u.during == "download" || u.during == "apply")
		switch {
		case u.state == "applying":
			return
		case u.state == "checking":
			u.downloadAfterCheck = u.downloadAfterCheck || download
			u.applyAfterDownload = u.applyAfterDownload || autoApply
			return
		case u.state == "downloading":
			u.applyAfterDownload = u.applyAfterDownload || autoApply
			refresh = !recent
			return
		case u.state == "ready":
			applyNow = autoApply
			refresh = !applyNow && !recent
			return
		case download && (u.state == "available" || failed):
			u.applyAfterDownload = u.applyAfterDownload || autoApply
			downloadNow = true
			return
		case u.state == "available":
			refresh = !recent
			return
		case recent:
			return
		}
		u.downloadAfterCheck = download
		u.applyAfterDownload = autoApply
		u.state, u.during, u.message = "checking", "", ""
		start = true
	})
	switch {
	case applyNow:
		u.apply(ctx)
		return
	case refresh:
		u.refresh(ctx)
		return
	case downloadNow:
		u.download(ctx)
		return
	case !start:
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	release, err := update.Latest(checkCtx, u.client, u.feed)
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
		downloadNow = u.downloadAfterCheck && u.state == "available"
		u.downloadAfterCheck = false
		if !downloadNow {
			u.applyAfterDownload = false
		}
	})
	if downloadNow {
		u.download(ctx)
	}
}

// newer asks for the latest release and returns it when it is newer than the one on offer. Without an answer
// the one on offer stands.
func (u *updater) newer(ctx context.Context) (update.Release, bool) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	release, err := update.Latest(ctx, u.client, u.feed)
	if err != nil || release.Version == "" {
		return update.Release{}, false
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.checked = time.Now()
	return release, update.Newer(release.Version, u.release.Version)
}

// refresh replaces an offer that has been overtaken. An update the person already asked for goes on with the
// newer version, so nobody has to install the old one first.
func (u *updater) refresh(ctx context.Context) {
	release, newer := u.newer(ctx)
	if !newer {
		return
	}
	again := false
	u.set(func() {
		if !update.Newer(release.Version, u.release.Version) {
			return
		}
		switch u.state {
		case "available":
			u.release = release
		case "ready", "downloading":
			u.release, u.path, u.state = release, "", "available"
			u.attempt++
			if u.cancel != nil {
				u.cancel()
			}
			again = true
		}
	})
	if again {
		u.download(ctx)
	}
}

// slowDownload is how long a download may take before the release is looked up once more at its end.
const slowDownload = 30 * time.Second

// download fetches the newest installer and checks it; when triggered by auto-apply it runs the installer
// immediately, otherwise the page offers "重启并更新".
func (u *updater) download(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	attempt := 0
	u.set(func() {
		if u.release.Version != "" && (u.state == "available" || (u.state == "error" && (u.during == "download" || u.during == "apply"))) {
			u.attempt++
			attempt, u.cancel = u.attempt, cancel
			u.state, u.during, u.done, u.total, u.message = "downloading", "", 0, u.release.Size, ""
		}
	})
	if attempt == 0 {
		return
	}
	// The offer may have been on screen for hours.
	release := u.take(ctx, attempt)
	var path string
	var err error
	for {
		started := time.Now()
		path, err = u.fetch(ctx, release, attempt)
		if err != nil || time.Since(started) < slowDownload {
			break
		}
		next := u.take(ctx, attempt)
		if next.Version == release.Version {
			break
		}
		release = next
	}
	var applyFn func(string)
	u.set(func() {
		// A newer release took over; its own download reports from here.
		if u.attempt != attempt {
			return
		}
		u.cancel = nil
		if err != nil {
			u.applyAfterDownload = false
			u.state, u.during, u.message = "error", "download", err.Error()
			return
		}
		u.path = path
		if u.applyAfterDownload && u.onApply != nil {
			u.state, u.during, u.message = "applying", "", ""
			applyFn = u.onApply
		} else {
			u.state = "ready"
		}
		u.applyAfterDownload = false
	})
	if applyFn != nil {
		applyFn(path)
	}
}

// take returns the release to download, moving the attempt on to a newer one when there is one.
func (u *updater) take(ctx context.Context, attempt int) update.Release {
	latest, newer := u.newer(ctx)
	var release update.Release
	u.set(func() {
		if newer && u.attempt == attempt {
			u.release, u.done, u.total = latest, 0, latest.Size
		}
		release = u.release
	})
	return release
}

func (u *updater) fetch(ctx context.Context, release update.Release, attempt int) (string, error) {
	progress := func(done, total int64) {
		u.set(func() {
			if u.attempt == attempt {
				u.done, u.total = done, total
			}
		})
	}
	download := release
	if u.feed == update.DefaultFeed {
		download.URL = "https://license.lfen.ink/download/native-update/v" + release.Version
	}
	path, err := update.Download(ctx, u.client, download, u.dir, progress)
	if err != nil && ctx.Err() == nil && download.URL != release.URL {
		path, err = update.Download(ctx, u.client, release, u.dir, progress)
	}
	return path, err
}

// apply runs the downloaded installer, or first gets a newer one that has come out since.
func (u *updater) apply(ctx context.Context) {
	var path string
	var run func(string)
	u.set(func() {
		if u.state == "ready" && u.path != "" && u.onApply != nil {
			path, run = u.path, u.onApply
			u.state, u.during, u.message = "applying", "", ""
		}
	})
	if run == nil {
		return
	}
	if release, newer := u.newer(ctx); newer {
		u.set(func() {
			u.release, u.path, u.state = release, "", "available"
			u.applyAfterDownload = true
		})
		u.download(ctx)
		return
	}
	run(path)
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
