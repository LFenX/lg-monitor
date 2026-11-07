//go:build !windows

package main

import (
	"fmt"
	"os"
)

type nativeAgentPaths struct{}

func (nativeAgentPaths) Inspect(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("使用技能路径不能包含链接：%s", path)
	}
	return info, nil
}
func (nativeAgentPaths) Replace(from, to string) error { return os.Rename(from, to) }
