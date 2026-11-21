//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package setup

import "os"

func replaceFile(from, to string) error { return os.Rename(from, to) }
