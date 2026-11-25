//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"context"
	"fmt"
	"os"
)

func detachOwnConsole() {}

// systemd starts and restarts the service itself.
func startInstalledService(context.Context) error { return nil }

func serviceFailure(context.Context) string { return "" }

func showNotice(message string) { fmt.Fprintln(os.Stderr, message) }
