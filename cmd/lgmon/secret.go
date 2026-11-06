package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"

	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type secretFactory func(string, *slog.Logger) (secrets.Store, error)
type secretInputError struct{ cause error }

func (e *secretInputError) Error() string {
	return "读取密钥失败，请通过终端或标准输入提供内容"
}
func (e *secretInputError) Unwrap() error { return e.cause }

func readSecret(input io.Reader) ([]byte, error) {
	var value []byte
	var err error
	if file, ok := input.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		value, err = term.ReadPassword(int(file.Fd()))
	} else {
		value, err = io.ReadAll(io.LimitReader(input, 65537))
	}
	if err != nil {
		clear(value)
		return nil, &secretInputError{cause: err}
	}
	if len(value) > 65536 {
		clear(value)
		return nil, fmt.Errorf("密钥不能超过 64KiB")
	}
	if len(value) > 0 && value[len(value)-1] == '\n' {
		value = value[:len(value)-1]
	}
	if len(value) > 0 && value[len(value)-1] == '\r' {
		value = value[:len(value)-1]
	}
	if len(value) == 0 {
		return nil, fmt.Errorf("密钥内容不能为空")
	}
	return value, nil
}

func newSecretCommand(factory secretFactory) *cobra.Command {
	command := &cobra.Command{Use: "secret", Short: "加密保存密钥或列出密钥名称"}
	open := func(cmd *cobra.Command) (secrets.Store, error) {
		home, err := cmd.Flags().GetString("home")
		if err != nil {
			return nil, fmt.Errorf("读取数据根目录失败：%w", err)
		}
		return factory(home, slog.Default())
	}
	var setJSON bool
	set := &cobra.Command{Use: "set <名称>", Short: "从标准输入读取密钥，终端输入不回显",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return &usageError{err}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := open(cmd)
			if err != nil {
				return err
			}
			value, err := readSecret(cmd.InOrStdin())
			if err != nil {
				return err
			}
			defer clear(value)
			if err := store.Set(cmd.Context(), args[0], value); err != nil {
				return err
			}
			if setJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
					OK bool `json:"ok"`
				}{true})
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "密钥已加密保存")
			if err != nil {
				return fmt.Errorf("输出密钥保存结果失败：%w", err)
			}
			return nil
		},
	}
	set.Flags().BoolVar(&setJSON, "json", false, "以 JSON 输出保存结果")
	var listJSON bool
	list := &cobra.Command{Use: "list", Short: "只列出密钥名称，不输出内容", Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := open(cmd)
			if err != nil {
				return err
			}
			names, err := store.List(cmd.Context())
			if err != nil {
				return err
			}
			if names == nil {
				names = []string{}
			}
			slices.Sort(names)
			if listJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
					Names []string `json:"names"`
				}{names})
			}
			for _, name := range names {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), name); err != nil {
					return fmt.Errorf("输出密钥名称失败：%w", err)
				}
			}
			return nil
		},
	}
	list.Flags().BoolVar(&listJSON, "json", false, "以 JSON 列出名称")
	command.AddCommand(set, list)
	return command
}
