//go:build !windows

package collector

import (
	"context"
	"fmt"
)

type nativeEventLogBackend struct{}

func (nativeEventLogBackend) Anchor(context.Context, string) (string, error) {
	return "", fmt.Errorf("事件日志采集仅支持 Windows")
}

func (nativeEventLogBackend) Open(context.Context, string, string, string, bool) (EventLogReader, error) {
	return nil, fmt.Errorf("事件日志采集仅支持 Windows")
}
