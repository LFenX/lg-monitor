//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import "github.com/LFenX/lg-monitor/internal/systemd"

func nativeServiceController() (serviceController, error) { return systemd.Native() }
