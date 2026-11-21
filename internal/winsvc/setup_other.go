//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package winsvc

import (
	"context"
	"fmt"
)

func SetupProgram() (string, error) { return "", fmt.Errorf("安装包仅支持 Windows x64") }
func LockSetup(context.Context) (func(), error) {
	return nil, fmt.Errorf("安装包仅支持 Windows x64")
}
func LockSetupWithName(context.Context, string) (func(), error) {
	return nil, fmt.Errorf("安装包仅支持 Windows x64")
}
func InstalledDefinition(context.Context) (Definition, bool, error) {
	return Definition{}, false, fmt.Errorf("安装包仅支持 Windows x64")
}
