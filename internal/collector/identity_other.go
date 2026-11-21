//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import "os"

func publishIdentity(temporary, destination string) error { return os.Link(temporary, destination) }

func identitySharingViolation(error) bool { return false }
