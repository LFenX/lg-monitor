//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

func setupGuardPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("安装路径必须为绝对路径")
	}
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("检查安装路径失败：%w", err)
		}
		if err == nil {
			attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
			if !ok || attributes.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
				return fmt.Errorf("安装路径及父目录不能含链接、重解析点或特殊文件")
			}
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return nil
}
