//go:build !windows

package collector

import (
	"context"
	"fmt"
	"time"
)

type nativeProcessEvents struct{ now func() time.Time }

func (nativeProcessEvents) Run(ctx context.Context, _ ProcessEventFilter, _ func(ProcessEvent)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("当前平台不支持 Windows 即时进程事件，使用间隔采样")
}
