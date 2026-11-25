// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

// Package durable replaces small state files so that a power cut leaves either the old or the new content.
package durable

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile replaces path with data. The bytes reach the disk before the new name does: a rename is journaled
// but file content is not, so without the flush a power cut can leave a file of zeros behind the new name.
func WriteFile(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("创建目录失败：%w", err)
	}
	file, err := os.CreateTemp(directory, filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败：%w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("写入临时文件失败：%w", err)
	}
	if err := replace(temporary, path); err != nil {
		return fmt.Errorf("替换 %s 失败：%w", filepath.Base(path), err)
	}
	return nil
}
