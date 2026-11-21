//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package config

import "os"

func replaceDocument(from, to string) error { return os.Rename(from, to) }
