package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "命令执行失败，请运行 lgmon --help 查看用法：%v\n", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	var home, logLevel string
	root := &cobra.Command{
		Use:           "lgmon",
		Short:         "Windows 服务监控",
		SilenceErrors: true,
		SilenceUsage:  true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			var level slog.Level
			if err := level.UnmarshalText([]byte(logLevel)); err != nil {
				return fmt.Errorf("日志级别 %q 无效，请使用 debug、info、warn 或 error：%w", logLevel, err)
			}
			slog.SetDefault(slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: level})))
			return nil
		},
	}
	root.PersistentFlags().StringVar(&home, "home", defaultHome(), "配置、数据和日志的根目录（可通过 LGMON_HOME 设置）")
	root.PersistentFlags().StringVar(&logLevel, "log-level", "info", "日志级别：debug、info、warn、error")
	root.AddCommand(newConfigCommand())
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "显示版本号、提交号和构建时间",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "版本：%s\n提交：%s\n构建时间：%s\n", version, commit, buildTime); err != nil {
				return fmt.Errorf("输出版本信息失败：%w", err)
			}
			return nil
		},
	})
	return root
}

func defaultHome() string {
	if home := os.Getenv("LGMON_HOME"); home != "" {
		return home
	}
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	return filepath.Join(programData, "lg-monitor")
}
