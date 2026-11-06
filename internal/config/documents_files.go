package config

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type DirectoryDocuments struct{ Path string }

func (d DirectoryDocuments) Open(name string) (fs.File, error) {
	if !validDocumentPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	return os.DirFS(d.Path).Open(name)
}

func (d DirectoryDocuments) ReadDir(name string) ([]fs.DirEntry, error) {
	return fs.ReadDir(os.DirFS(d.Path), name)
}

func (d DirectoryDocuments) Replace(ctx context.Context, name string, data []byte) error {
	if !validDocumentPath(name) {
		return fmt.Errorf("配置文件路径无效")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("配置写入已取消：%w", err)
	}
	destination := filepath.Join(d.Path, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("创建配置目录失败：%w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".lgmon-config-*")
	if err != nil {
		return fmt.Errorf("创建临时配置文件失败：%w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("写入临时配置失败：%w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("刷新临时配置失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭临时配置失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("配置发布已取消：%w", err)
	}
	if err := replaceDocument(temporary, destination); err != nil {
		return fmt.Errorf("原子替换配置失败：%w", err)
	}
	return nil
}

func (d DirectoryDocuments) Remove(ctx context.Context, name string) error {
	if !validDocumentPath(name) {
		return fmt.Errorf("配置文件路径无效")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("配置删除已取消：%w", err)
	}
	if err := os.Remove(filepath.Join(d.Path, filepath.FromSlash(name))); err != nil {
		return fmt.Errorf("删除配置文件失败：%w", err)
	}
	return nil
}
