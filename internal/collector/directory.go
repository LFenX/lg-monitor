package collector

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type workingDirectoryReader interface {
	ReadDirectory(context.Context, ProcessIdentity) (string, error)
}

type nativeWorkingDirectoryReader struct{}

type workingDirectorySample struct {
	at    time.Time
	value string
	err   error
}

func (s *processSource) WorkingDirectory(ctx context.Context, identity ProcessIdentity) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("工作目录读取已取消：%w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.details[identity]; !exists {
		return "", fmt.Errorf("进程身份已退出或改变，请等待下一次进程采集")
	}
	at := s.now()
	if cached, exists := s.workingDirs[identity]; exists && at.Sub(cached.at) >= 0 && at.Sub(cached.at) < 10*time.Second {
		return cached.value, cached.err
	}
	value, err := s.directories.ReadDirectory(ctx, identity)
	if err == nil && value == "" {
		// Access-denied PEB reads can return an empty string without an error.
		err = fmt.Errorf("工作目录读取为空，无法确认目录匹配")
	}
	if err != nil {
		err = fmt.Errorf("读取 PID %d 的工作目录失败：%w", identity.PID, err)
		value = ""
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		s.workingDirs[identity] = workingDirectorySample{at: at, value: value, err: err}
	}
	return value, err
}
