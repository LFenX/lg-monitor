// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import "github.com/LFenX/lg-monitor/internal/winsvc"

func nativeServiceController() (serviceController, error) { return winsvc.NativeController() }
