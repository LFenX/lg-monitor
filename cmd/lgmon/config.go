package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCommand() *cobra.Command {
	command := &cobra.Command{Use: "config", Short: "校验配置或生成配置 Schema"}
	command.AddCommand(&cobra.Command{
		Use: "validate", Short: "校验配置目录中的全部配置", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := cmd.Flags().GetString("home")
			if err != nil {
				return fmt.Errorf("读取配置根目录参数失败：%w", err)
			}
			if _, err := config.Load(os.DirFS(filepath.Join(home, "config"))); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "配置校验通过")
			if err != nil {
				return fmt.Errorf("输出校验结果失败：%w", err)
			}
			return nil
		},
	})
	var output string
	schema := &cobra.Command{
		Use: "schema", Short: "从配置结构体生成 JSON Schema", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			names, err := config.WriteSchemas(output)
			if err != nil {
				return err
			}
			for _, name := range names {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "已生成：%s\n", filepath.Join(output, name)); err != nil {
					return fmt.Errorf("输出 Schema 结果失败：%w", err)
				}
			}
			return nil
		},
	}
	schema.Flags().StringVar(&output, "output", "schemas", "Schema 输出目录")
	command.AddCommand(schema)
	return command
}
