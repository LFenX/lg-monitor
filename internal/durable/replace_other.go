//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package durable

import (
	"os"
	"path/filepath"
)

func replace(temporary, destination string) error {
	if err := os.Rename(temporary, destination); err != nil {
		return err
	}
	// The new name lives in the directory, which has its own cache.
	if directory, err := os.Open(filepath.Dir(destination)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}
