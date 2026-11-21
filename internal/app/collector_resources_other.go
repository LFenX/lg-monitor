//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

func applyCollectorResidentBudget() (func() error, error) { return func() error { return nil }, nil }
