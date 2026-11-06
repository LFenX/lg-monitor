//go:build windows

package winsvc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/LFenX/lg-monitor/internal/secrets"
	"golang.org/x/sys/windows"
)

type nativeFiles struct {
	policy    secrets.PathPolicy
	directory string
}

func (f nativeFiles) Prepare(ctx context.Context, definition Definition) (Definition, error) {
	if err := ctx.Err(); err != nil {
		return definition, err
	}
	directory := f.directory
	if directory == "" {
		root, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
		if err != nil {
			return definition, fmt.Errorf("读取 Program Files 目录失败：%w", err)
		}
		directory = filepath.Join(root, "lg-monitor")
	}
	if err := f.policy.ProtectTree(directory, true); err != nil {
		return definition, err
	}
	destination := filepath.Join(directory, "lgmon.exe")
	source, err := os.Open(definition.Executable)
	if err != nil {
		return definition, fmt.Errorf("打开待安装程序失败：%w", err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return definition, fmt.Errorf("读取待安装程序失败：%w", err)
	}
	if !info.Mode().IsRegular() {
		return definition, fmt.Errorf("待安装程序必须是普通文件")
	}
	existing, err := os.Stat(destination)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return definition, fmt.Errorf("读取安装目标失败：%w", err)
	}
	if existing == nil || !os.SameFile(info, existing) {
		temporary, err := os.CreateTemp(directory, ".lgmon-*.exe")
		if err != nil {
			return definition, fmt.Errorf("创建程序安装副本失败：%w", err)
		}
		name := temporary.Name()
		defer os.Remove(name)
		_, copyErr := io.Copy(temporary, source)
		if copyErr == nil {
			copyErr = temporary.Sync()
		}
		copyErr = errors.Join(copyErr, temporary.Close(), ctx.Err())
		if copyErr != nil {
			return definition, fmt.Errorf("复制程序失败：%w", copyErr)
		}
		if err := os.Rename(name, destination); err != nil {
			return definition, fmt.Errorf("发布程序安装副本失败：%w", err)
		}
	}
	if err := f.policy.ProtectTree(directory, true); err != nil {
		return definition, err
	}
	if err := f.policy.Check(directory); err != nil {
		return definition, err
	}
	if err := f.policy.Check(destination); err != nil {
		return definition, err
	}
	if err := f.policy.ProtectTree(definition.Home, false); err != nil {
		return definition, err
	}
	definition.Executable = destination
	return definition, nil
}
