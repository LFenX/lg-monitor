//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package notify

import "context"

type unsupportedToast struct{}

func nativeToastPlatform() ToastPlatform { return unsupportedToast{} }

func (unsupportedToast) Deliver(context.Context, []byte) (ToastResult, error) {
	return ToastResult{Skipped: true, Reason: "当前平台不支持 Windows 通知"}, nil
}
