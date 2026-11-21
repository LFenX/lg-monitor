//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"fmt"
	"os"
)

func detachOwnConsole() {}

func showNotice(message string) { fmt.Fprintln(os.Stderr, message) }
