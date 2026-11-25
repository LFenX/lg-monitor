//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package ports

import (
	"context"
	"errors"
)

// Reserved is a Windows matter; elsewhere a refused bind is about privileges, which another port must not hide.
func Reserved(error) bool { return false }

func netsh(context.Context, ...string) (string, error) {
	return "", errors.New("端口登记只适用于 Windows")
}
