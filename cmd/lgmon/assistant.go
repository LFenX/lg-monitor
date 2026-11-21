// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// newAssistantCommand configures 波西米亚 on this machine without going through HTTP, so a key never has to pass
// through a browser or a chat.
func newAssistantCommand(factory secretFactory) *cobra.Command {
	root := &cobra.Command{Use: "assistant", Short: "配置波西米亚（面板内助手）：DeepSeek 与百炼的密钥和模型"}
	var model, region, embedding, rerank, keys string
	var setJSON bool
	set := &cobra.Command{Use: "set", Short: "保存模型与地域；密钥从标准输入读取，终端输入不回显，留空表示沿用已保存的", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result := struct {
			OK             bool   `json:"ok"`
			Model          string `json:"model,omitempty"`
			Region         string `json:"region,omitempty"`
			EmbeddingModel string `json:"embeddingModel,omitempty"`
			RerankModel    string `json:"rerankModel,omitempty"`
			DeepSeekKey    bool   `json:"deepseekKey"`
			DashScopeKey   bool   `json:"dashscopeKey"`
			Error          string `json:"error,omitempty"`
		}{}
		work := func() error {
			var wanted []string
			switch keys {
			case "both":
				wanted = []string{config.DeepSeekKeyRef, config.DashScopeKeyRef}
			case "deepseek":
				wanted = []string{config.DeepSeekKeyRef}
			case "dashscope":
				wanted = []string{config.DashScopeKeyRef}
			case "none":
			default:
				return &usageError{fmt.Errorf("--key 必须是 both、deepseek、dashscope 或 none")}
			}
			home, _ := cmd.Flags().GetString("home")
			path := filepath.Join(home, "config", "settings.yaml")
			old, err := os.ReadFile(path)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("读取 settings.yaml 失败：%w", err)
			}
			current, err := config.ParseSettings(old)
			if err != nil {
				return err
			}
			settings := current.Assistant
			for flag, field := range map[string]*string{"model": &settings.Model, "region": &settings.Region, "embedding-model": &settings.EmbeddingModel, "rerank-model": &settings.RerankModel} {
				if cmd.Flags().Changed(flag) {
					value, _ := cmd.Flags().GetString(flag)
					*field = strings.TrimSpace(value)
				}
			}
			if err := settings.Check(); err != nil {
				return &usageError{err}
			}
			updated, err := config.SetAssistant(old, settings)
			if err != nil {
				return err
			}
			names := map[string]string{config.DeepSeekKeyRef: "DeepSeek", config.DashScopeKeyRef: "百炼"}
			terminal := false
			if file, ok := cmd.InOrStdin().(*os.File); ok && file == os.Stdin && !setJSON {
				terminal = true
			}
			if len(wanted) > 0 {
				store, err := factory(home, slog.Default())
				if err != nil {
					return err
				}
				lines := bufio.NewReader(cmd.InOrStdin())
				for _, ref := range wanted {
					if terminal {
						if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "粘贴 %s 的 API Key 后按回车（输入不会显示，直接回车表示沿用已保存的）：", names[ref]); err != nil {
							return err
						}
					}
					value, err := readKeyLine(cmd.InOrStdin(), lines)
					if err != nil {
						return err
					}
					if len(value) == 0 && len(wanted) == 1 {
						return fmt.Errorf("密钥内容不能为空")
					}
					if len(value) > 0 {
						if err := store.Set(cmd.Context(), ref, value); err != nil {
							clear(value)
							return err
						}
					}
					clear(value)
				}
				for ref, saved := range map[string]*bool{config.DeepSeekKeyRef: &result.DeepSeekKey, config.DashScopeKeyRef: &result.DashScopeKey} {
					value, err := store.Get(cmd.Context(), ref)
					*saved = err == nil && len(value) > 0
					clear(value)
				}
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return fmt.Errorf("创建配置目录失败：%w", err)
			}
			temporary := path + ".lgmon-tmp"
			if err := os.WriteFile(temporary, updated, 0o600); err != nil {
				return fmt.Errorf("写入 settings.yaml 失败：%w", err)
			}
			if err := os.Rename(temporary, path); err != nil {
				os.Remove(temporary)
				return fmt.Errorf("替换 settings.yaml 失败：%w", err)
			}
			result.Model, result.Region, result.EmbeddingModel, result.RerankModel = settings.EffectiveModel(), settings.EffectiveRegion(), settings.EffectiveEmbeddingModel(), settings.EffectiveRerankModel()
			return nil
		}
		failure := work()
		result.OK = failure == nil
		if failure != nil {
			result.Error = failure.Error()
		}
		if setJSON {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
				return fmt.Errorf("输出助手设置结果失败：%w", err)
			}
			if failure != nil {
				return &reportedError{failure}
			}
			return nil
		}
		if failure != nil {
			return failure
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "\n波西米亚已设置：DeepSeek %s，百炼 %s（%s / %s）。正在运行的监控器会自动加载；密钥是否有效，在网页“设置 → 波西米亚”点“测试”核对。\n", result.Model, result.Region, result.EmbeddingModel, result.RerankModel)
		return err
	}}
	set.Flags().StringVar(&model, "model", "", "DeepSeek 对话模型，省略为 deepseek-chat")
	set.Flags().StringVar(&region, "region", "", "百炼地域：beijing（默认）或 singapore")
	set.Flags().StringVar(&embedding, "embedding-model", "", "百炼向量模型，省略为 "+config.DefaultEmbeddingModel)
	set.Flags().StringVar(&rerank, "rerank-model", "", "百炼 rerank 模型，省略为 "+config.DefaultRerankModel)
	set.Flags().StringVar(&keys, "key", "both", "读取哪些密钥：both、deepseek、dashscope 或 none（只改模型）")
	set.Flags().BoolVar(&setJSON, "json", false, "以 JSON 输出结果")
	root.AddCommand(set)
	return root
}

// readKeyLine reads one key: hidden from a terminal, otherwise one line, so several keys can be piped in order.
func readKeyLine(input io.Reader, lines *bufio.Reader) ([]byte, error) {
	if file, ok := input.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		value, err := term.ReadPassword(int(file.Fd()))
		if err != nil {
			return nil, &secretInputError{cause: err}
		}
		return bytes.TrimSpace(value), nil
	}
	value, err := lines.ReadBytes(byte(10))
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, &secretInputError{cause: err}
	}
	if len(value) > 65536 {
		clear(value)
		return nil, fmt.Errorf("密钥不能超过 64KiB")
	}
	return bytes.TrimSpace(value), nil
}
