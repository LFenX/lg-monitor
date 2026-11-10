package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/spf13/cobra"
)

// newAssistantCommand configures the in-panel assistant on this machine without going through HTTP,
// so the key never has to pass through a browser or a chat.
func newAssistantCommand(factory secretFactory) *cobra.Command {
	root := &cobra.Command{Use: "assistant", Short: "选择面板内 AI 助手的模型供应商并保存 API Key"}
	var providersJSON bool
	providers := &cobra.Command{Use: "providers", Short: "列出可用的模型供应商、默认地址与推荐模型", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if providersJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(config.AssistantProviders)
		}
		for _, item := range config.AssistantProviders {
			key := "需要 API Key"
			if !item.NeedsKey {
				key = "无需 API Key"
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%-12s %-22s %-8s 推荐模型 %-28s %s\n", item.ID, item.Name, item.Protocol, item.Model, key); err != nil {
				return err
			}
		}
		return nil
	}}
	providers.Flags().BoolVar(&providersJSON, "json", false, "以 JSON 输出供应商列表")

	var provider, model, address string
	var setJSON, noKey bool
	set := &cobra.Command{Use: "set", Short: "保存供应商、模型和地址；需要密钥时从标准输入读取，终端输入不回显", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result := struct {
			OK       bool   `json:"ok"`
			Provider string `json:"provider,omitempty"`
			Model    string `json:"model,omitempty"`
			BaseURL  string `json:"baseUrl,omitempty"`
			KeyRef   string `json:"keyRef,omitempty"`
			Error    string `json:"error,omitempty"`
		}{}
		work := func() error {
			settings := config.AssistantSettings{Provider: provider, Model: strings.TrimSpace(model), BaseURL: strings.TrimSpace(address)}
			if err := settings.Check(); err != nil {
				return &usageError{err}
			}
			catalog, _ := config.FindAssistantProvider(provider)
			home, _ := cmd.Flags().GetString("home")
			path := filepath.Join(home, "config", "settings.yaml")
			old, err := os.ReadFile(path)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("读取 settings.yaml 失败：%w", err)
			}
			updated, err := config.SetAssistant(old, settings)
			if err != nil {
				return err
			}
			if catalog.NeedsKey && !noKey {
				if file, ok := cmd.InOrStdin().(*os.File); ok && file == os.Stdin && !setJSON {
					if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "粘贴 %s 的 API Key 后按回车（输入不会显示）：", catalog.Name); err != nil {
						return err
					}
				}
				value, err := readSecret(cmd.InOrStdin())
				if err != nil {
					return err
				}
				defer clear(value)
				store, err := factory(home, slog.Default())
				if err != nil {
					return err
				}
				if err := store.Set(cmd.Context(), settings.EffectiveKeyRef(), value); err != nil {
					return err
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
			result.Provider, result.Model, result.BaseURL, result.KeyRef = settings.EffectiveProvider(), settings.EffectiveModel(), settings.EffectiveBaseURL(), settings.EffectiveKeyRef()
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
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "\nAI 助手已改为 %s · %s（%s）。正在运行的监控器会自动加载，回到网页点“问 AI”即可。\n", result.Provider, result.Model, result.BaseURL)
		return err
	}}
	set.Flags().StringVar(&provider, "provider", "anthropic", "供应商 ID，见 lgmon assistant providers")
	set.Flags().StringVar(&model, "model", "", "模型 ID；省略时用推荐模型")
	set.Flags().StringVar(&address, "base-url", "", "接口地址；省略时用供应商默认地址，custom 必填")
	set.Flags().BoolVar(&noKey, "no-key", false, "不读取密钥，沿用已保存的 API Key")
	set.Flags().BoolVar(&setJSON, "json", false, "以 JSON 输出结果")
	root.AddCommand(providers, set)
	return root
}
