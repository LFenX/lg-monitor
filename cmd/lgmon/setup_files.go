package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/winsvc"
)

type setupFileStore struct {
	Source string
	Paths  secrets.PathPolicy
	Guard  func(string) error
}

func setupCanonical(path string) (string, error) {
	part := filepath.Clean(path)
	var missing []string
	for {
		_, err := os.Lstat(part)
		if err == nil {
			canonical, err := filepath.EvalSymlinks(part)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				canonical = filepath.Join(canonical, missing[i])
			}
			return canonical, nil
		}
		if !errors.Is(err, os.ErrNotExist) || filepath.Dir(part) == part {
			return "", err
		}
		missing = append(missing, filepath.Base(part))
		part = filepath.Dir(part)
	}
}

func setupContains(parent, child string) bool {
	if !strings.EqualFold(filepath.VolumeName(parent), filepath.VolumeName(child)) {
		return false
	}
	rel, err := filepath.Rel(strings.ToLower(parent), strings.ToLower(child))
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

func (f setupFileStore) Check(ctx context.Context, home, program string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if f.Paths == nil || f.Guard == nil {
		return false, fmt.Errorf("安装路径保护未设置")
	}
	for _, path := range []string{home, filepath.Dir(program), f.Source} {
		if err := f.Guard(path); err != nil {
			return false, fmt.Errorf("安装路径检查失败：%w", err)
		}
	}
	if source, err := os.Stat(f.Source); err != nil || !source.Mode().IsRegular() {
		return false, fmt.Errorf("安装载荷不存在或不是普通文件")
	}
	canonicalHome, err := setupCanonical(home)
	if err != nil {
		return false, fmt.Errorf("解析安装数据目录失败：%w", err)
	}
	canonicalProgram, err := setupCanonical(filepath.Dir(program))
	if err != nil {
		return false, fmt.Errorf("解析程序目录失败：%w", err)
	}
	if setupContains(canonicalHome, canonicalProgram) || setupContains(canonicalProgram, canonicalHome) {
		return false, fmt.Errorf("程序和数据目录解析后相同或互相包含，拒绝安装")
	}
	if target, err := os.Stat(program); err == nil {
		source, err := os.Stat(f.Source)
		if err != nil || !target.Mode().IsRegular() || !os.SameFile(source, target) {
			return false, fmt.Errorf("安装目标已有 lgmon.exe，没有覆盖；请先使用原卸载方式移除程序并保留 home")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("读取安装目标失败：%w", err)
	}
	entries, err := os.ReadDir(home)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("读取现有数据目录失败：%w", err)
	}
	if len(entries) == 0 {
		return false, nil
	}
	known := false
	for _, name := range []string{"config/settings.yaml", "config/targets.yaml", "config/rules.yaml", "config/notifiers.yaml", "data/pki/authority.json", "data/pki/collector.json", "data/lgmon.db", "data/outbox.db", "data/setup/installation.json"} {
		info, err := os.Lstat(filepath.Join(home, filepath.FromSlash(name)))
		if err == nil && info.Mode().IsRegular() {
			known = true
			break
		}
	}
	if !known {
		return false, fmt.Errorf("所选非空目录不包含可识别的 lgmon 配置或身份/数据，拒绝接管其他目录")
	}
	if _, err := config.Load(os.DirFS(filepath.Join(home, "config"))); err != nil {
		return false, fmt.Errorf("已有配置无效，未覆盖或启动服务：%w", err)
	}
	return true, nil
}

func (f setupFileStore) Initialize(ctx context.Context, home string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := f.Guard(home); err != nil {
		return err
	}
	if err := f.Paths.ProtectTree(home, false); err != nil {
		return fmt.Errorf("保护安装数据目录失败：%w", err)
	}
	directory := filepath.Join(home, "config")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("创建初始配置目录失败：%w", err)
	}
	initial := []struct{ name, data string }{
		{"settings.yaml", "httpListen: 127.0.0.1:7444\nremoteOperations:\n  enabled: false\n"},
		{"targets.yaml", "targets: []\n"},
		{"rules.yaml", "rules: []\n"},
		{"notifiers.yaml", "feishuApps: []\nnotifiers: []\n"},
	}
	for _, document := range initial {
		if err := ctx.Err(); err != nil {
			return err
		}
		file, err := os.OpenFile(filepath.Join(directory, document.name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("创建缺失配置 %s 失败：%w", document.name, err)
		}
		_, writeErr := io.WriteString(file, document.data)
		if writeErr == nil {
			writeErr = file.Sync()
		}
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return fmt.Errorf("写入初始配置 %s 失败：%w", document.name, err)
		}
	}
	if _, err := config.Load(os.DirFS(directory)); err != nil {
		return fmt.Errorf("初始化后的配置校验失败：%w", err)
	}
	if err := os.MkdirAll(filepath.Join(home, "data", "setup"), 0o700); err != nil {
		return fmt.Errorf("创建安装回执目录失败：%w", err)
	}
	return f.Paths.ProtectTree(home, false)
}

func (f setupFileStore) SaveReceipt(ctx context.Context, home string, receipt winsvc.SetupReceipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Join(home, "data", "setup")
	if err := f.Paths.Check(directory); err != nil {
		return fmt.Errorf("安装回执目录不安全：%w", err)
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("编码安装回执失败：%w", err)
	}
	temporary, err := os.CreateTemp(directory, ".receipt-*")
	if err != nil {
		return fmt.Errorf("创建安装回执失败：%w", err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	_, writeErr := temporary.Write(append(data, '\n'))
	if writeErr == nil {
		writeErr = temporary.Sync()
	}
	if err := errors.Join(writeErr, temporary.Close()); err != nil {
		return fmt.Errorf("保存安装回执失败：%w", err)
	}
	if err := f.Paths.ProtectTree(directory, false); err != nil {
		return fmt.Errorf("保护安装回执失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(name, filepath.Join(directory, "installation.json")); err != nil {
		return fmt.Errorf("发布安装回执失败：%w", err)
	}
	return nil
}

type setupPairInput struct {
	Code        string `json:"code"`
	Fingerprint string `json:"fingerprint"`
}

func setupInbox(directory string, paths secrets.PathPolicy, guard func(string) error) error {
	if !filepath.IsAbs(directory) || filepath.Dir(directory) == directory {
		return fmt.Errorf("安装输入目录必须是新的绝对路径，不能为根目录")
	}
	if err := guard(filepath.Dir(directory)); err != nil {
		return err
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return fmt.Errorf("创建新的安装输入目录失败，不接管既有目录：%w", err)
	}
	// Precreate the input so the wizard can overwrite it without changing its trusted owner.
	file, err := os.OpenFile(filepath.Join(directory, "pair.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("创建受保护输入占位文件失败：%w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭受保护输入占位文件失败：%w", err)
	}
	if err := paths.ProtectTree(directory, false); err != nil {
		return fmt.Errorf("保护安装输入目录失败：%w", err)
	}
	return paths.Check(directory)
}

func readSetupPairInput(reader io.Reader) (setupPairInput, error) {
	data, err := io.ReadAll(io.LimitReader(reader, 4097))
	if err != nil || len(data) > 4096 {
		return setupPairInput{}, fmt.Errorf("读取配对输入失败，内容最多 4096 字节")
	}
	defer clear(data)
	trimmed := strings.TrimSpace(string(data))
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return setupPairInput{}, fmt.Errorf("配对输入必须是只包含 code/fingerprint 的 JSON 对象")
	}
	var value setupPairInput
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("配对输入必须是只包含 code/fingerprint 的有效 JSON")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return value, fmt.Errorf("配对输入只能包含一个 JSON 对象")
	}
	return value, nil
}

func readSetupPairFile(path string, paths secrets.PathPolicy, guard func(string) error) (setupPairInput, error) {
	if !filepath.IsAbs(path) {
		return setupPairInput{}, fmt.Errorf("配对输入文件必须使用绝对路径")
	}
	if err := guard(path); err != nil {
		return setupPairInput{}, fmt.Errorf("配对输入路径不安全：%w", err)
	}
	for _, item := range []string{filepath.Dir(path), path} {
		if err := paths.Check(item); err != nil {
			return setupPairInput{}, fmt.Errorf("配对输入必须由 SYSTEM/Administrators 保护：%w", err)
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return setupPairInput{}, fmt.Errorf("打开配对输入失败：%w", err)
	}
	value, readErr := readSetupPairInput(file)
	closeErr := file.Close()
	if readErr != nil {
		return value, readErr
	}
	if err := errors.Join(closeErr, os.Remove(path)); err != nil {
		return setupPairInput{}, fmt.Errorf("清理已读取的配对输入失败，没有注册服务：%w", err)
	}
	return value, nil
}
