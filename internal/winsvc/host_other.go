//go:build !windows

package winsvc

import (
	"context"
	"fmt"
	"log/slog"
)

type Runtime struct {
	Logger   *slog.Logger
	Ready    func()
	Identity string
}

func IsService() (bool, error) { return false, nil }
func RunService(context.Context, string, slog.Level, func(context.Context, Runtime) error) error {
	return fmt.Errorf("Windows 服务宿主仅支持 Windows")
}
