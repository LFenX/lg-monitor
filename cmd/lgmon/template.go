package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/spf13/cobra"
)

func templateArgs(minimum, maximum int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < minimum || len(args) > maximum {
			return &usageError{fmt.Errorf("%s 需要 %d～%d 个参数", cmd.CommandPath(), minimum, maximum)}
		}
		return nil
	}
}

func commandRenderer(cmd *cobra.Command) (*cards.Renderer, error) {
	home, err := cmd.Flags().GetString("home")
	if err != nil {
		return nil, fmt.Errorf("读取配置根目录失败：%w", err)
	}
	manager, err := config.NewManager(config.FSLoader{FS: os.DirFS(filepath.Join(home, "config"))}, slog.Default())
	if err != nil {
		return nil, err
	}
	return cards.New(manager, cards.Options{})
}

func newTemplateCommand() *cobra.Command {
	command := &cobra.Command{Use: "template", Short: "列出、校验、预览或创建飞书卡片模板"}
	var jsonOutput bool
	command.PersistentFlags().BoolVar(&jsonOutput, "json", false, "输出模板结果 JSON，preview 保留原卡片 JSON")
	list := &cobra.Command{Use: "list", Short: "列出内置和自定义模板", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			renderer, err := commandRenderer(cmd)
			if err != nil {
				return err
			}
			if jsonOutput {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(renderer.List()); err != nil {
					return fmt.Errorf("输出模板列表失败：%w", err)
				}
				return nil
			}
			for _, entry := range renderer.List() {
				kind := "自定义"
				if entry.Preset {
					kind = "预设"
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", entry.ID, kind, entry.Name); err != nil {
					return fmt.Errorf("输出模板列表失败：%w", err)
				}
			}
			return nil
		}}
	command.AddCommand(list)
	command.AddCommand(&cobra.Command{Use: "validate [id]", Short: "以触发、恢复和汇总样例校验模板", Args: templateArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			renderer, err := commandRenderer(cmd)
			if err != nil {
				return err
			}
			entries := renderer.List()
			if len(args) == 1 {
				entries = []cards.Entry{{ID: args[0]}}
			}
			var failures int
			results := []templateValidationResult{}
			for _, entry := range entries {
				if err := renderer.Check(entry.ID); err != nil {
					failures++
					if jsonOutput {
						results = append(results, templateValidationResult{entry.ID, false, err.Error()})
						continue
					}
					if _, writeErr := fmt.Fprintln(cmd.ErrOrStderr(), err); writeErr != nil {
						return fmt.Errorf("输出模板校验错误失败：%w", writeErr)
					}
				} else if jsonOutput {
					results = append(results, templateValidationResult{entry.ID, true, ""})
				} else if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s：三种样例校验通过\n", entry.ID); err != nil {
					return fmt.Errorf("输出模板校验结果失败：%w", err)
				}
			}
			if jsonOutput {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
					Valid   bool `json:"valid"`
					Results any  `json:"results"`
				}{failures == 0, results}); err != nil {
					return err
				}
			}
			if failures > 0 {
				return &reportedError{fmt.Errorf("%d 个模板校验失败", failures)}
			}
			return nil
		}})
	var state string
	preview := &cobra.Command{Use: "preview <id>", Short: "输出样例卡片 JSON", Args: templateArgs(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if state != "firing" && state != "resolved" && state != "digest" {
				return &usageError{fmt.Errorf("--state 须为 firing、resolved 或 digest")}
			}
			renderer, err := commandRenderer(cmd)
			if err != nil {
				return err
			}
			result := renderer.Render(args[0], cards.Sample(state))
			if result.Warning != "" {
				if _, err := fmt.Fprintln(cmd.ErrOrStderr(), result.Warning); err != nil {
					return fmt.Errorf("输出模板回退提示失败：%w", err)
				}
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(result.JSON)); err != nil {
				return fmt.Errorf("输出卡片 JSON 失败：%w", err)
			}
			return nil
		}}
	preview.Flags().StringVar(&state, "state", "firing", "样例状态：firing、resolved 或 digest")
	command.AddCommand(preview)
	var from string
	create := &cobra.Command{Use: "new <id>", Short: "生成带注释的自定义模板配置", Args: templateArgs(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			renderer, err := cards.New(nil, cards.Options{})
			if err != nil {
				return err
			}
			data, err := renderer.Scaffold(args[0], from)
			if err != nil {
				return &usageError{err}
			}
			home, err := cmd.Flags().GetString("home")
			if err != nil {
				return fmt.Errorf("读取配置根目录失败：%w", err)
			}
			directory := filepath.Join(home, "config", "card-templates")
			if err := os.MkdirAll(directory, 0o700); err != nil {
				return fmt.Errorf("创建模板目录失败：%w", err)
			}
			path := filepath.Join(directory, args[0]+".yaml")
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return fmt.Errorf("创建模板失败，已有文件不会被覆盖：%w", err)
			}
			_, writeErr := file.Write(data)
			closeErr := file.Close()
			if writeErr != nil {
				return fmt.Errorf("写入模板失败：%w", writeErr)
			}
			if closeErr != nil {
				return fmt.Errorf("关闭模板文件失败：%w", closeErr)
			}
			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
					Status string `json:"status"`
					ID     string `json:"id"`
					Path   string `json:"path"`
				}{"ok", args[0], path})
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "已生成：%s\n", path); err != nil {
				return fmt.Errorf("输出模板路径失败：%w", err)
			}
			return nil
		}}
	create.Flags().StringVar(&from, "from", "standard", "基础预设：standard、compact、detailed 或 digest")
	command.AddCommand(create)
	return command
}
