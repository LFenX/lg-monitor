package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/app"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/transport"
	"github.com/LFenX/lg-monitor/internal/winsvc"
)

type setupPrivateError struct {
	message string
	cause   error
}

func (e setupPrivateError) Error() string { return e.message }
func (e setupPrivateError) Unwrap() error { return e.cause }

type nativeSetupPairer struct{ now func() time.Time }

func (p nativeSetupPairer) Paired(ctx context.Context, home, monitor string) (bool, error) {
	_, err := os.Stat(filepath.Join(home, "data", "pki", "collector.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, setupPrivateError{"无法读取已有采集器身份，没有覆盖或重新配对", err}
	}
	vault, err := secrets.New(home, slog.Default())
	if err != nil {
		return false, err
	}
	identity, err := transport.LoadClient(ctx, transport.FileState{Home: home}, vault, p.now)
	if err != nil {
		return false, setupPrivateError{"已有配对身份无效，请恢复同一份身份与密钥；安装不会覆盖它", err}
	}
	if !strings.EqualFold(identity.Monitor, monitor) {
		return false, fmt.Errorf("已有采集器身份属于另一个监控器，拒绝覆盖；请保留原 home 或另选新的 home")
	}
	return true, nil
}

func (p nativeSetupPairer) Join(ctx context.Context, home, monitor, code, fingerprint string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := defaultPairDependencies().Join(ctx, home, monitor, code, fingerprint, nil); err != nil {
		return setupPrivateError{"采集器配对未完成，请核对地址、配对码和 CA 指纹；没有注册服务", err}
	}
	return nil
}

func nativeSetupDependencies(guard func(string) error) setupDependencies {
	paths := secrets.NativePathPolicy()
	return setupDependencies{Administrator: winsvc.IsAdministrator, Paths: paths, Guard: guard, Panel: func(home string) (string, error) {
		settings, err := config.LoadSettings(os.DirFS(filepath.Join(home, "config")))
		if err != nil {
			return "", err
		}
		return app.TrayMonitorURL(settings.HTTPAddress())
	}, Factory: func(context.Context) (*winsvc.Setup, error) {
		if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
			return nil, fmt.Errorf("安装包当前仅支持 Windows 10/11 x64")
		}
		program, err := winsvc.SetupProgram()
		if err != nil {
			return nil, err
		}
		source, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("定位安装程序载荷失败：%w", err)
		}
		controller, err := winsvc.NativeController()
		if err != nil {
			return nil, err
		}
		return winsvc.NewSetup(winsvc.SetupOptions{Service: controller, Files: setupFileStore{Source: source, Paths: paths, Guard: guard}, Pairer: nativeSetupPairer{now: time.Now}, Binding: winsvc.InstalledDefinition, Lock: winsvc.LockSetup, Now: time.Now, Source: source, Program: program, Version: version})
	}}
}
