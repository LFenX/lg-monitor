//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import (
	"context"
	"fmt"
)

func (nativeWorkingDirectoryReader) ReadDirectory(context.Context, ProcessIdentity) (string, error) {
	return "", fmt.Errorf("当前平台不支持读取 Windows 进程的工作目录")
}
