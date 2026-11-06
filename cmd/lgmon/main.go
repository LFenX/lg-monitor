package main

import (
	"errors"
	"fmt"
	"io"
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
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}

type usageError struct{ error }

func (e *usageError) Unwrap() error { return e.error }

type reportedError struct{ error }

func (e *reportedError) Unwrap() error { return e.error }

func noArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.NoArgs(cmd, args); err != nil {
		return &usageError{err}
	}
	return nil
}

func execute(args []string, stdout, stderr io.Writer) int {
	root := newRootCommand()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.Execute(); err != nil {
		var reported *reportedError
		if errors.As(err, &reported) {
			return 1
		}
		var usage *usageError
		if errors.As(err, &usage) {
			fmt.Fprintf(stderr, "参数或用法错误，请运行 lgmon --help 查看用法：%v\n", err)
		} else {
			fmt.Fprintf(stderr, "命令执行失败：%v\n", err)
		}
		return 1
	}
	return 0
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
				return &usageError{fmt.Errorf("日志级别 %q 无效，请使用 debug、info、warn 或 error：%w", logLevel, err)}
			}
			slog.SetDefault(slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{Level: level})))
			return nil
		},
	}
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error { return &usageError{err} })
	root.PersistentFlags().StringVar(&home, "home", defaultHome(), "配置、数据和日志的根目录（可通过 LGMON_HOME 设置）")
	root.PersistentFlags().StringVar(&logLevel, "log-level", "info", "日志级别：debug、info、warn、error")
	root.AddCommand(newConfigCommand())
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "显示版本号、提交号和构建时间",
		Args:  noArgs,
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
