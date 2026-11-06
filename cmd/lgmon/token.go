package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/winsvc"
	"github.com/spf13/cobra"
)

type tokenStore interface {
	AddAPIToken(context.Context, []byte, string, string) (store.APIToken, error)
	ListAPITokens(context.Context) ([]store.APIToken, error)
	RevokeAPIToken(context.Context, string) error
	Close() error
}

type tokenDependencies struct {
	Open          func(context.Context, string) (tokenStore, error)
	Administrator func() (bool, error)
	Entropy       io.Reader
}

func defaultTokenDependencies() tokenDependencies {
	return tokenDependencies{Administrator: winsvc.IsAdministrator, Entropy: rand.Reader, Open: func(ctx context.Context, home string) (tokenStore, error) {
		dataPath, err := filepath.Abs(filepath.Join(home, "data"))
		if err != nil {
			return nil, fmt.Errorf("解析认证数据目录失败：%w", err)
		}
		if err := secrets.NativePathPolicy().ProtectTree(dataPath, false); err != nil {
			return nil, fmt.Errorf("保护认证数据目录失败：%w", err)
		}
		backend, err := store.OpenAuthDB(ctx, filepath.Join(dataPath, "auth.db"), time.Now)
		if errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("生成服务令牌需要以管理员身份运行终端：%w", err)
		}
		return backend, err
	}}
}

type tokenOutput struct {
	Status    string            `json:"status"`
	Message   string            `json:"message"`
	Token     string            `json:"token,omitempty"`
	Name      string            `json:"name,omitempty"`
	Scope     api.Scope         `json:"scope,omitempty"`
	CreatedAt int64             `json:"createdAt,omitempty"`
	ID        string            `json:"id,omitempty"`
	Tokens    *[]store.APIToken `json:"tokens,omitempty"`
}

func requireTokenAdmin(deps tokenDependencies) error {
	if deps.Administrator == nil {
		return fmt.Errorf("令牌管理需要管理员身份检查器")
	}
	admin, err := deps.Administrator()
	if err != nil {
		return fmt.Errorf("检查令牌管理员权限失败：%w", err)
	}
	if !admin {
		return fmt.Errorf("令牌管理需要以管理员身份运行终端")
	}
	return nil
}

func newTokenCommand(deps tokenDependencies) *cobra.Command {
	root := &cobra.Command{Use: "token", Short: "生成 HTTP API 访问令牌"}
	var scope, name string
	var asJSON bool
	create := &cobra.Command{Use: "create", Short: "以管理员身份生成令牌：read 用于查看，config 可确认和静音", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result := tokenOutput{Status: "ok", Message: "令牌仅显示这一次，请妥善保存"}
		issue := func() error {
			permission := api.Scope(scope)
			if !permission.Valid() {
				return fmt.Errorf("scope 必须为 read、config 或 admin")
			}
			if err := requireTokenAdmin(deps); err != nil {
				return err
			}
			token, err := api.NewToken(permission, name, deps.Entropy)
			if err != nil {
				return err
			}
			home, _ := cmd.Flags().GetString("home")
			backend, err := deps.Open(cmd.Context(), home)
			if err != nil {
				return err
			}
			row, saveErr := backend.AddAPIToken(cmd.Context(), token.Digest[:], token.Name, string(token.Scope))
			if err := errors.Join(saveErr, backend.Close()); err != nil {
				return err
			}
			result.Token, result.Name, result.Scope, result.CreatedAt = token.Value, token.Name, token.Scope, row.CreatedAt
			result.ID = row.ID
			return nil
		}
		failure := issue()
		if failure != nil {
			result.Status, result.Message = "error", failure.Error()
		}
		if asJSON {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
				return fmt.Errorf("输出令牌结果失败：%w", err)
			}
			if failure != nil {
				return &reportedError{failure}
			}
			return nil
		}
		if failure != nil {
			return failure
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n名称：%s\n权限：%s\n%s\n", result.Message, result.Name, result.Scope, result.Token)
		return err
	}}
	create.Flags().StringVar(&scope, "scope", "read", "read 只查看；config 可确认、静音和改配置；admin 可改 settings 和密钥")
	create.Flags().StringVar(&name, "name", "", "令牌名称，例如 phone")
	create.Flags().BoolVar(&asJSON, "json", false, "输出令牌结果 JSON")
	root.AddCommand(create, newTokenMetadataCommand("list", deps), newTokenMetadataCommand("revoke", deps))
	return root
}

func newTokenMetadataCommand(action string, deps tokenDependencies) *cobra.Command {
	var asJSON bool
	use, short, args := action, "以管理员身份列出令牌元数据", noArgs
	if action == "revoke" {
		use = "revoke <令牌ID>"
		short = "以管理员身份吊销令牌"
		args = func(_ *cobra.Command, values []string) error {
			if len(values) != 1 || values[0] == "" {
				return &usageError{fmt.Errorf("请指定要吊销的令牌 ID")}
			}
			return nil
		}
	}
	command := &cobra.Command{Use: use, Short: short, Args: args, RunE: func(cmd *cobra.Command, values []string) error {
		result := tokenOutput{Status: "ok", Message: "令牌清单不包含令牌原文或哈希"}
		work := func() error {
			if err := requireTokenAdmin(deps); err != nil {
				return err
			}
			home, _ := cmd.Flags().GetString("home")
			backend, err := deps.Open(cmd.Context(), home)
			if err != nil {
				return err
			}
			if action == "list" {
				rows, err := backend.ListAPITokens(cmd.Context())
				closeErr := backend.Close()
				if err = errors.Join(err, closeErr); err != nil {
					return err
				}
				result.Tokens = &rows
			} else {
				err := backend.RevokeAPIToken(cmd.Context(), values[0])
				closeErr := backend.Close()
				if err = errors.Join(err, closeErr); err != nil {
					return err
				}
				result.ID = values[0]
				result.Message = "令牌已吊销"
			}
			return nil
		}
		failure := work()
		if failure != nil {
			result.Status, result.Message = "error", failure.Error()
		}
		if asJSON {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
				return fmt.Errorf("输出令牌元数据失败：%w", err)
			}
			if failure != nil {
				return &reportedError{failure}
			}
			return nil
		}
		if failure != nil {
			return failure
		}
		if result.Tokens != nil {
			for _, row := range *result.Tokens {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t最后使用：%s\t已吊销=%t\n", row.ID, row.Name, row.Scope, cliTime(row.LastUsedAt), row.RevokedAt != 0); err != nil {
					return err
				}
			}
			return nil
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), result.Message)
		return err
	}}
	command.Flags().BoolVar(&asJSON, "json", false, "输出令牌元数据 JSON")
	return command
}
