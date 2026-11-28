// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/update"
)

const (
	housekeepingInterval     = 6 * time.Hour
	housekeepingInitialDelay = 15 * time.Second
	housekeepingLogMaxBytes  = 10 * 1024 * 1024
	housekeepingTempMaxAge   = time.Hour
)

type autoCleanTracker struct {
	mu     sync.Mutex
	status api.StorageAutoCleanStatus
}

func (t *autoCleanTracker) record(at int64, freed int64, removedFiles int) {
	t.mu.Lock()
	t.status.LastRunAt = at
	t.status.LastFreedBytes = freed
	t.status.RemovedFiles = removedFiles
	t.status.Runs++
	t.mu.Unlock()
}

func (t *autoCleanTracker) snapshot() api.StorageAutoCleanStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

func (b *APIBackend) runStorageHousekeeping(ctx context.Context) {
	if strings.TrimSpace(b.options.Home) == "" {
		return
	}
	timer := time.NewTimer(housekeepingInitialDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	b.runHousekeepingPass()

	ticker := time.NewTicker(housekeepingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.runHousekeepingPass()
		}
	}
}

func (b *APIBackend) runHousekeepingPass() {
	home := filepath.Clean(strings.TrimSpace(b.options.Home))
	if home == "" {
		return
	}
	now := b.options.Now()
	removed, freed := cleanupHomeHousekeeping(home, b.options.Version, now)
	b.autoClean.record(now.UnixMilli(), freed, removed)
	if removed > 0 || freed > 0 {
		b.options.Logger.Info("Completed periodic storage housekeeping", "removedFiles", removed, "freedBytes", freed)
	}
}

func cleanupHomeHousekeeping(home, currentVersion string, now time.Time) (int, int64) {
	var removed int
	var freed int64

	// 1. Trim log files exceeding 10 MiB down to the latest 2 MiB of complete lines.
	logsDir := filepath.Join(home, "logs")
	if entries, err := os.ReadDir(logsDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".log") {
				continue
			}
			full := filepath.Join(logsDir, entry.Name())
			if info, statErr := os.Stat(full); statErr == nil && info.Size() > housekeepingLogMaxBytes {
				before := info.Size()
				trimSingleLogInPlace(full, logCompactKeepBytes)
				if afterInfo, afterErr := os.Stat(full); afterErr == nil && before > afterInfo.Size() {
					freed += before - afterInfo.Size()
				}
			}
		}
	}

	// 2. Remove stale temporary artifacts older than 1 hour.
	_ = filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		name := strings.ToLower(entry.Name())
		if !strings.HasSuffix(name, ".tmp") && name != ".lgmon-write-probe" {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil || now.Sub(info.ModTime()) < housekeepingTempMaxAge {
			return nil
		}
		if os.Remove(path) == nil {
			removed++
			freed += info.Size()
		}
		return nil
	})

	// 3. Remove superseded installer executables in <home>/updates and default user cache dir.
	for _, dir := range installerUpdateDirs(home) {
		r, f := update.PruneInstallers(dir, "", currentVersion)
		removed += r
		freed += f
	}
	return removed, freed
}

func installerUpdateDirs(home string) []string {
	if home == "" {
		return nil
	}
	return []string{filepath.Join(filepath.Clean(home), "updates")}
}
