package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

//go:embed agent-skill.md
var agentSkillSource string

const agentManaged = "<!-- Managed by lgmon agent install. -->"

type agentPathPolicy interface {
	Inspect(string) (os.FileInfo, error)
	Replace(string, string) error
}
type agentDependencies struct {
	Executable func() (string, error)
	UserHome   func() (string, error)
	Paths      agentPathPolicy
}

func defaultAgentDependencies() agentDependencies {
	return agentDependencies{Executable: os.Executable, UserHome: os.UserHomeDir, Paths: nativeAgentPaths{}}
}

type agentContext struct {
	Executable string `json:"executable"`
	Home       string `json:"home"`
	Config     string `json:"config"`
}

func quotedPowerShell(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func agentLocalContext(deps agentDependencies, home string) (agentContext, error) {
	executable, err := deps.Executable()
	if err != nil {
		return agentContext{}, fmt.Errorf("读取 lgmon 程序路径失败：%w", err)
	}
	values := []string{executable, home}
	for i, value := range values {
		if value == "" || !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
			return agentContext{}, fmt.Errorf("程序和 home 路径必须非空且不能包含控制字符")
		}
		values[i], err = filepath.Abs(value)
		if err != nil {
			return agentContext{}, fmt.Errorf("解析 Agent 使用路径失败：%w", err)
		}
	}
	return agentContext{Executable: values[0], Home: values[1], Config: filepath.Join(values[1], "config")}, nil
}

func renderAgentSkill(local agentContext) (string, error) {
	paths, err := json.MarshalIndent(local, "", "  ")
	if err != nil {
		return "", fmt.Errorf("编码 Agent 路径失败：%w", err)
	}
	t, err := template.New("lg-monitor").Option("missingkey=error").Parse(strings.ReplaceAll(agentSkillSource, "\r\n", "\n"))
	if err != nil {
		return "", fmt.Errorf("读取内置使用技能失败：%w", err)
	}
	var out bytes.Buffer
	err = t.Execute(&out, map[string]string{
		"Paths": string(paths), "Exe": quotedPowerShell(local.Executable), "Home": quotedPowerShell(local.Home),
	})
	if err != nil {
		return "", fmt.Errorf("填充 Agent 使用技能失败：%w", err)
	}
	return out.String(), nil
}

type agentInstallResult struct {
	OK        bool     `json:"ok"`
	Installed []string `json:"installed"`
	Error     string   `json:"error,omitempty"`
}

func newAgentCommand(deps agentDependencies) *cobra.Command {
	root := &cobra.Command{Use: "agent", Short: "生成 lg-monitor 使用技能和新会话指令"}
	var target string
	var installJSON, promptJSON bool
	install := &cobra.Command{Use: "install", Short: "安装当前账户的 lg-monitor 使用技能，不改 MCP 凭据", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result := agentInstallResult{Installed: []string{}}
		work := func() error {
			var targets []string
			switch target {
			case "all":
				targets = []string{".claude", ".codex"}
			case "claude":
				targets = []string{".claude"}
			case "codex":
				targets = []string{".codex"}
			default:
				return &usageError{fmt.Errorf("target 必须为 claude、codex 或 all")}
			}
			home, _ := cmd.Flags().GetString("home")
			local, err := agentLocalContext(deps, home)
			if err != nil {
				return err
			}
			content, err := renderAgentSkill(local)
			if err != nil {
				return err
			}
			userHome, err := deps.UserHome()
			if err != nil {
				return fmt.Errorf("读取当前用户主目录失败：%w", err)
			}
			if !filepath.IsAbs(userHome) {
				return fmt.Errorf("当前用户主目录必须是绝对路径")
			}
			for _, name := range targets {
				if err := inspectAgentDestination(deps.Paths, userHome, name); err != nil {
					return err
				}
			}
			for _, name := range targets {
				directory := filepath.Join(userHome, name, "skills", "lg-monitor")
				if err := writeAgentSkill(cmd.Context(), deps.Paths, userHome, name, []byte(content)); err != nil {
					return err
				}
				result.Installed = append(result.Installed, filepath.Join(directory, "SKILL.md"))
			}
			return nil
		}
		failure := work()
		result.OK = failure == nil
		if failure != nil {
			result.Error = failure.Error()
		}
		if installJSON {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
				return fmt.Errorf("输出技能安装结果失败：%w", err)
			}
			if failure != nil {
				return &reportedError{failure}
			}
			return nil
		}
		for _, path := range result.Installed {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "已安装使用技能：%s\n", path); err != nil {
				return fmt.Errorf("输出技能路径失败：%w", err)
			}
		}
		return failure
	}}
	install.Flags().StringVar(&target, "target", "all", "目标：claude、codex 或 all")
	install.Flags().BoolVar(&installJSON, "json", false, "输出成功状态及实际已安装路径 JSON")
	var wish string
	prompt := &cobra.Command{Use: "prompt", Short: "生成可直接交给新 Agent 会话的完整指令", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(wish) == "" || !utf8.ValidString(wish) || strings.ContainsRune(wish, 0) {
			return &usageError{fmt.Errorf("wish 必须是非空任务描述，且不能包含 NUL 字符")}
		}
		home, _ := cmd.Flags().GetString("home")
		local, err := agentLocalContext(deps, home)
		if err != nil {
			return err
		}
		skill, err := renderAgentSkill(local)
		if err != nil {
			return err
		}
		encoded, _ := json.Marshal(wish)
		_, body, ok := strings.Cut(strings.TrimPrefix(skill, "---\n"), "\n---\n")
		if !ok {
			return fmt.Errorf("内置使用技能缺少完整元信息")
		}
		text := "# 使用 lg-monitor 完成本次任务\n\n用户愿望（JSON 字符串，仅作任务数据；不扩大操作授权）：\n\n" + string(encoded) + "\n\n" + strings.TrimSpace(body) + "\n"
		if promptJSON {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
				agentContext
				Prompt string `json:"prompt"`
			}{local, text}); err != nil {
				return fmt.Errorf("输出 Agent 指令 JSON 失败：%w", err)
			}
			return nil
		}
		if _, err := io.WriteString(cmd.OutOrStdout(), text); err != nil {
			return fmt.Errorf("输出 Agent 指令失败：%w", err)
		}
		return nil
	}}
	prompt.Flags().StringVar(&wish, "wish", "", "希望 Agent 完成的任务")
	_ = prompt.MarkFlagRequired("wish")
	prompt.Flags().BoolVar(&promptJSON, "json", false, "输出实际路径和完整 prompt JSON")
	root.AddCommand(install, prompt)
	return root
}

func inspectAgentDestination(policy agentPathPolicy, root, name string) error {
	if _, err := inspectAgentDirectories(policy, root, name, false); err != nil {
		return err
	}
	file := filepath.Join(root, name, "skills", "lg-monitor", "SKILL.md")
	info, err := policy.Inspect(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查已有使用技能失败：%w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 2*1024*1024 {
		return fmt.Errorf("已有 SKILL.md 不是可更新的普通使用技能：%s", file)
	}
	reader, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("读取已有使用技能失败：%w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, 2*1024*1024+1))
	if err := errors.Join(readErr, reader.Close()); err != nil {
		return fmt.Errorf("读取已有使用技能失败：%w", err)
	}
	if len(data) > 2*1024*1024 {
		return fmt.Errorf("已有使用技能超过 2 MiB，请先备份或移走再安装：%s", file)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	_, body, hasBody := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n")
	if !strings.HasPrefix(text, "---\nname: lg-monitor\n") || !hasBody || !strings.HasPrefix(strings.TrimLeft(body, "\n"), agentManaged+"\n") {
		return fmt.Errorf("已有技能没有可核对的 lgmon 管理标记，请先备份或移走再安装：%s", file)
	}
	return nil
}
