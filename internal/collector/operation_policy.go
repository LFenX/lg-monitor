package collector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/secrets"
)

type OperationProgram interface{ Path() (string, error) }
type NativeOperationProgram struct{}

func (NativeOperationProgram) Path() (string, error) { return os.Executable() }

type DiskOperationPolicy struct {
	Home     string
	Identity string
	Paths    secrets.PathPolicy
	Program  OperationProgram
}

func (p DiskOperationPolicy) ReadOperationPolicy(ctx context.Context) (config.RemoteOperations, error) {
	if err := ctx.Err(); err != nil {
		return config.RemoteOperations{}, fmt.Errorf("读取操作授权已取消：%w", err)
	}
	settings, err := config.LoadSettings(os.DirFS(filepath.Join(p.Home, "config")))
	if err != nil {
		return config.RemoteOperations{}, err
	}
	policy := settings.RemoteOperations
	if !policy.Enabled {
		return policy, nil
	}
	if p.Identity != "LocalSystem" || p.Paths == nil || p.Program == nil {
		return config.RemoteOperations{}, fmt.Errorf("服务操作只允许受保护的 LocalSystem 服务身份")
	}
	program, err := p.Program.Path()
	if err != nil {
		return config.RemoteOperations{}, fmt.Errorf("读取服务操作程序路径失败：%w", err)
	}
	for _, path := range []string{program, p.Home, filepath.Join(p.Home, "config"), filepath.Join(p.Home, "config", "settings.yaml"), filepath.Join(p.Home, "data")} {
		if err := p.Paths.Check(path); err != nil {
			return config.RemoteOperations{}, fmt.Errorf("服务操作路径或权限不安全：%w", err)
		}
	}
	return policy, nil
}
