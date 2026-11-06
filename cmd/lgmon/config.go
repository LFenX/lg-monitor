package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCommand() *cobra.Command {
	command := &cobra.Command{Use: "config", Short: "校验配置或生成配置 Schema"}
	var jsonOutput bool
	validate := &cobra.Command{
		Use: "validate", Short: "校验配置目录中的全部配置", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := cmd.Flags().GetString("home")
			if err != nil {
				return fmt.Errorf("读取配置根目录参数失败：%w", err)
			}
			_, loadErr := config.Load(os.DirFS(filepath.Join(home, "config")))
			issues := make(config.Errors, 0)
			if loadErr != nil && !errors.As(loadErr, &issues) {
				return loadErr
			}
			var writeErr error
			if jsonOutput {
				writeErr = json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
					OK     bool          `json:"ok"`
					Errors config.Errors `json:"errors"`
				}{OK: loadErr == nil, Errors: issues})
			} else if loadErr != nil {
				_, writeErr = fmt.Fprintf(cmd.ErrOrStderr(), "配置有 %d 处错误：\n%s\n", len(issues), issues.Error())
			} else {
				_, writeErr = fmt.Fprintln(cmd.OutOrStdout(), "配置校验通过")
			}
			if writeErr != nil {
				return fmt.Errorf("输出校验结果失败：%w", writeErr)
			}
			if loadErr != nil {
				return &reportedError{loadErr}
			}
			return nil
		},
	}
	validate.Flags().BoolVar(&jsonOutput, "json", false, "以 JSON 输出校验结果")
	command.AddCommand(validate)
	var output string
	schema := &cobra.Command{
		Use: "schema", Short: "从配置结构体生成 JSON Schema", Args: noArgs,
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
