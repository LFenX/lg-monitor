//go:build !windows

package main

import (
	"fmt"
	"github.com/spf13/cobra"
)

func installerFixtureRootCommand() *cobra.Command { return nil }

func platformSetupDependencies() setupDependencies {
	return nativeSetupDependencies(func(string) error { return fmt.Errorf("安装包仅支持 Windows") })
}
