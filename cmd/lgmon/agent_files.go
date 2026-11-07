package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func inspectAgentDirectories(policy agentPathPolicy, root, name string, create bool) (string, error) {
	directory := filepath.Join(root, name, "skills", "lg-monitor")
	current := filepath.Clean(root)
	info, err := policy.Inspect(current)
	if err != nil {
		return "", fmt.Errorf("检查用户主目录失败：%w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("用户主目录不是普通目录：%s", current)
	}
	for _, part := range []string{name, "skills", "lg-monitor"} {
		current = filepath.Join(current, part)
		info, err = policy.Inspect(current)
		if errors.Is(err, os.ErrNotExist) {
			if !create {
				return directory, nil
			}
			if err := os.Mkdir(current, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return "", fmt.Errorf("创建使用技能目录失败：%w", err)
			}
			info, err = policy.Inspect(current)
		}
		if err != nil {
			return "", fmt.Errorf("检查使用技能目录失败：%w", err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("使用技能路径不是普通目录：%s", current)
		}
	}
	return directory, nil
}

func writeAgentSkill(ctx context.Context, policy agentPathPolicy, root, name string, content []byte) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("技能安装已取消：%w", err)
	}
	if err := inspectAgentDestination(policy, root, name); err != nil {
		return err
	}
	dir, err := inspectAgentDirectories(policy, root, name, true)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".lgmon-skill-")
	if err != nil {
		return fmt.Errorf("创建使用技能临时文件失败：%w", err)
	}
	temp := file.Name()
	defer os.Remove(temp)
	failure := file.Chmod(0600)
	if failure == nil {
		_, failure = file.Write(content)
	}
	if failure == nil {
		failure = file.Sync()
	}
	if err := errors.Join(failure, file.Close()); err != nil {
		return fmt.Errorf("写入使用技能失败：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("技能安装已取消：%w", err)
	}
	if err := policy.Replace(temp, filepath.Join(dir, "SKILL.md")); err != nil {
		return fmt.Errorf("保存使用技能失败：%w", err)
	}
	return nil
}
