//go:build !windows

package notify

import "context"

type unsupportedToast struct{}

func nativeToastPlatform() ToastPlatform { return unsupportedToast{} }

func (unsupportedToast) Deliver(context.Context, []byte) (ToastResult, error) {
	return ToastResult{Skipped: true, Reason: "当前平台不支持 Windows 通知"}, nil
}
