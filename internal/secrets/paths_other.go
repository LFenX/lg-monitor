//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package secrets

import "fmt"

type pathPolicy struct{}

func NativePathPolicy() PathPolicy   { return pathPolicy{} }
func Personal() bool                 { return false }
func OwnedByCurrentUser(string) bool { return false }
func (pathPolicy) Check(string) error {
	return fmt.Errorf("Windows 路径权限检查仅支持 Windows")
}
func (pathPolicy) ProtectTree(string, bool) error {
	return fmt.Errorf("Windows 路径权限设置仅支持 Windows")
}
