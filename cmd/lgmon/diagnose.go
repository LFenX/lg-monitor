package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/spf13/cobra"
)

type diagnosticReader interface {
	DiagnosticPackage(context.Context, string) (store.DiagnosticPackage, error)
	Close() error
}
type diagnosticFactory func(context.Context, string) (diagnosticReader, error)

func openDiagnostic(ctx context.Context, home string) (diagnosticReader, error) {
	return store.OpenView(ctx, filepath.Join(home, "data", "lgmon.db"))
}
func newDiagnoseCommand(open diagnosticFactory) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{Use: "diagnose <告警ID>", Short: "只读生成告警元数据诊断包，不读取文件内容或执行修复", Args: func(_ *cobra.Command, args []string) error {
		if len(args) != 1 {
			return &usageError{fmt.Errorf("请提供一个实际告警 ID")}
		}
		return nil
	}, RunE: func(cmd *cobra.Command, args []string) error {
		home, _ := cmd.Flags().GetString("home")
		reader, err := open(cmd.Context(), home)
		var value store.DiagnosticPackage
		if err == nil {
			value, err = reader.DiagnosticPackage(cmd.Context(), args[0])
			err = errors.Join(err, reader.Close())
		}
		if err != nil {
			if asJSON {
				if outputErr := json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"error": map[string]string{"code": "diagnostic_failed", "message": err.Error()}}); outputErr != nil {
					return fmt.Errorf("输出诊断错误失败：%w", outputErr)
				}
				return &reportedError{err}
			}
			return fmt.Errorf("生成诊断包失败：%w", err)
		}
		if asJSON {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(value); err != nil {
				return fmt.Errorf("输出诊断 JSON 失败：%w", err)
			}
			return nil
		}
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return fmt.Errorf("编码诊断交接文本失败：%w", err)
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n\n%s\n", value.AgentPrompt, data); err != nil {
			return fmt.Errorf("输出诊断交接文本失败：%w", err)
		}
		return nil
	}}
	command.Flags().BoolVar(&asJSON, "json", false, "输出版本化 JSON 诊断包")
	return command
}
