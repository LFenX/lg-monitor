// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
)

// HomeRedirectFile lives inside the base home directory (for example C:\ProgramData\lg-monitor)
// when the user migrates lgmon's data storage to another drive or folder.
const HomeRedirectFile = "storage-redirect.json"

type HomeRedirect struct {
	Home         string `json:"home"`
	PreviousHome string `json:"previousHome,omitempty"`
	MigratedAt   int64  `json:"migratedAt,omitempty"`
}

// DefaultBaseHome returns lgmon's platform default home directory before applying any redirect or LGMON_HOME override.
func DefaultBaseHome() string {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	return filepath.Join(programData, "lg-monitor")
}

// SamePath reports whether two filesystem paths refer to the same location after cleaning.
func SamePath(a, b string) bool {
	ca, cb := filepath.Clean(strings.TrimSpace(a)), filepath.Clean(strings.TrimSpace(b))
	if ca == "" || cb == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ca, cb)
	}
	return ca == cb
}

// ReadHomeRedirect loads the redirect pointer from baseHome if present and valid.
func ReadHomeRedirect(baseHome string) (HomeRedirect, bool) {
	if strings.TrimSpace(baseHome) == "" {
		return HomeRedirect{}, false
	}
	data, err := os.ReadFile(filepath.Join(baseHome, HomeRedirectFile))
	if err != nil {
		return HomeRedirect{}, false
	}
	var redirect HomeRedirect
	if err := json.Unmarshal(data, &redirect); err != nil {
		return HomeRedirect{}, false
	}
	target := filepath.Clean(strings.TrimSpace(redirect.Home))
	if target == "" || !filepath.IsAbs(target) || strings.HasPrefix(target, `\\`) || strings.ContainsFunc(target, unicode.IsControl) {
		return HomeRedirect{}, false
	}
	redirect.Home = target
	return redirect, true
}

// ResolveHome checks whether baseHome contains a valid redirect pointing to an existing directory.
// Single-hop resolution avoids redirect loops while letting both CLI commands and existing Windows
// service registrations follow a migrated data directory automatically on restart.
func ResolveHome(baseHome string) (string, bool) {
	redirect, ok := ReadHomeRedirect(baseHome)
	if !ok {
		return baseHome, false
	}
	if SamePath(baseHome, redirect.Home) {
		return baseHome, false
	}
	info, err := os.Stat(redirect.Home)
	if err != nil || !info.IsDir() {
		return baseHome, false
	}
	return redirect.Home, true
}

// WriteHomeRedirect atomically records that baseHome has been migrated to targetHome.
func WriteHomeRedirect(baseHome, targetHome, previousHome string, at time.Time) error {
	base := filepath.Clean(strings.TrimSpace(baseHome))
	target := filepath.Clean(strings.TrimSpace(targetHome))
	if base == "" || !filepath.IsAbs(target) || strings.HasPrefix(target, `\\`) || strings.ContainsFunc(target, unicode.IsControl) {
		return fmt.Errorf("存储重定向目标路径无效")
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return fmt.Errorf("创建存储指针目录失败：%w", err)
	}
	payload := HomeRedirect{
		Home:         target,
		PreviousHome: filepath.Clean(strings.TrimSpace(previousHome)),
		MigratedAt:   at.UnixMilli(),
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("编码存储指针失败：%w", err)
	}
	data = append(data, '\n')
	dest := filepath.Join(base, HomeRedirectFile)
	temp := dest + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return fmt.Errorf("写入存储指针失败：%w", err)
	}
	if err := os.Rename(temp, dest); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("保存存储指针失败：%w", err)
	}
	return nil
}
