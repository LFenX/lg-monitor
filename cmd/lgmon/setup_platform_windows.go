//go:build windows && !installerfixture

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import "github.com/spf13/cobra"

func platformSetupDependencies() setupDependencies { return nativeSetupDependencies(setupGuardPath) }
func installerFixtureRootCommand() *cobra.Command  { return nil }
