//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package secrets

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// On Linux and other Unix systems root plays the part of Administrators and SYSTEM: a home root owns is a
// service home, and any other account works in a home of its own.

const foreignHomeMessage = "这个数据目录属于 root（系统服务），当前账户无权读写。请在命令前加 sudo，或用 --home 指定你自己的目录"

type pathPolicy struct{}

func NativePathPolicy() PathPolicy { return pathPolicy{} }

// Personal reports whether protected paths belong to the current account rather than root.
func Personal() bool { return os.Geteuid() != 0 }

func ownerOf(info os.FileInfo) (uint32, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Uid, true
}

// OwnedByCurrentUser reports whether an existing path belongs to the current non-root account.
func OwnedByCurrentUser(path string) bool {
	if os.Geteuid() == 0 {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	owner, ok := ownerOf(info)
	return ok && owner == uint32(os.Geteuid())
}

func ordinaryPath(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("读取受保护路径失败：%w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return nil, fmt.Errorf("受保护路径不能是链接或特殊文件：%s", path)
	}
	return info, nil
}

// Check accepts a path only root or the current account owns, and that nobody else may write to.
func (pathPolicy) Check(path string) error {
	info, err := ordinaryPath(path)
	if err != nil {
		return err
	}
	owner, ok := ownerOf(info)
	if !ok {
		return fmt.Errorf("读取路径所有者失败：%s", path)
	}
	if owner != 0 && owner != uint32(os.Geteuid()) {
		return fmt.Errorf("路径所有者不是 root 或当前账户：%s", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("组或其他账户可以修改受保护路径：%s", path)
	}
	return nil
}

func (pathPolicy) ProtectTree(path string, readable bool) error {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) || path == string(os.PathSeparator) {
		return fmt.Errorf("受保护目录必须是绝对路径，且不能是根目录")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrPermission) || errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("%s：%w", foreignHomeMessage, os.ErrPermission)
		}
		return fmt.Errorf("创建受保护目录失败：%w", err)
	}
	dirMode, fileMode := os.FileMode(0o700), os.FileMode(0o600)
	if readable {
		dirMode, fileMode = 0o755, 0o644
	}
	self := uint32(os.Geteuid())
	return filepath.WalkDir(path, func(current string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrPermission) {
				return fmt.Errorf("%s：%w", foreignHomeMessage, walkErr)
			}
			return fmt.Errorf("遍历受保护目录失败：%w", walkErr)
		}
		info, err := ordinaryPath(current)
		if err != nil {
			return err
		}
		if owner, ok := ownerOf(info); ok && self != 0 && owner != self {
			return fmt.Errorf("%s（%s）：%w", foreignHomeMessage, current, os.ErrPermission)
		}
		mode := fileMode
		if info.IsDir() {
			mode = dirMode
		}
		if info.Mode().Perm() != mode {
			if err := os.Chmod(current, mode); err != nil {
				return fmt.Errorf("设置受保护路径权限失败：%w", err)
			}
		}
		return nil
	})
}
