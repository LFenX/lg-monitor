//go:build windows && !installerfixture

package main

import "github.com/spf13/cobra"

func platformSetupDependencies() setupDependencies { return nativeSetupDependencies(setupGuardPath) }
func installerFixtureRootCommand() *cobra.Command  { return nil }
