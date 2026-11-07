//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

type nativeAgentPaths struct{}

func (nativeAgentPaths) Inspect(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	attrs, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok || attrs.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, fmt.Errorf("使用技能路径不能包含链接或重解析点：%s", path)
	}
	return info, nil
}
func (nativeAgentPaths) Replace(from, to string) error {
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return fmt.Errorf("解析使用技能临时路径失败：%w", err)
	}
	target, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return fmt.Errorf("解析使用技能保存路径失败：%w", err)
	}
	return windows.MoveFileEx(source, target, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
