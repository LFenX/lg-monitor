package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type FileState struct{ Home string }

func (f FileState) path(name string) (string, error) {
	if name != "authority" && name != "collector" && name != "endpoint" {
		return "", fmt.Errorf("未知的证书状态名称")
	}
	return filepath.Join(f.Home, "data", "pki", name+".json"), nil
}

func (f FileState) Load(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("读取证书状态已取消：%w", err)
	}
	path, err := f.path(name)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开证书状态失败：%w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err != nil {
		return nil, fmt.Errorf("读取证书状态失败：%w", err)
	}
	if len(data) > 1024*1024 {
		return nil, fmt.Errorf("证书状态超过 1 MiB 上限")
	}
	return data, nil
}

func (f FileState) Save(ctx context.Context, name string, data []byte, overwrite bool) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("保存证书状态已取消：%w", err)
	}
	path, err := f.path(name)
	if err != nil {
		return err
	}
	if len(data) > 1024*1024 {
		return fmt.Errorf("证书状态超过 1 MiB 上限")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(filepath.Dir(directory), 0o700); err != nil {
		return fmt.Errorf("创建证书目录失败：%w", err)
	}
	createErr := os.Mkdir(directory, 0o700)
	if createErr != nil && !errors.Is(createErr, os.ErrExist) {
		return fmt.Errorf("创建证书信任目录失败：%w", createErr)
	}
	if err := secureStateDirectory(directory, createErr == nil); err != nil {
		return fmt.Errorf("保护证书信任目录失败：%w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".pki-*")
	if err != nil {
		return fmt.Errorf("创建证书临时文件失败：%w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return fmt.Errorf("保护证书状态文件失败：%w", err)
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("写入证书状态失败：%w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("刷新证书状态失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭证书状态文件失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("发布证书状态已取消：%w", err)
	}
	if err := publishState(temporary, path, overwrite); err != nil {
		return fmt.Errorf("发布证书状态失败：%w", err)
	}
	return nil
}
