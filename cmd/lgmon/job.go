package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/spf13/cobra"
)

type jobKeyStore interface {
	SetJobKey(context.Context, string, []byte) error
	Close() error
}

type jobKeyDependencies struct {
	Open          func(context.Context, string) (jobKeyStore, error)
	Administrator func() (bool, error)
	Entropy       io.Reader
}

func defaultJobKeyDependencies() jobKeyDependencies {
	tokens := defaultTokenDependencies()
	return jobKeyDependencies{Administrator: tokens.Administrator, Entropy: rand.Reader, Open: func(ctx context.Context, home string) (jobKeyStore, error) {
		backend, err := tokens.Open(ctx, home)
		if err != nil {
			return nil, err
		}
		keys, ok := backend.(jobKeyStore)
		if !ok {
			backend.Close()
			return nil, fmt.Errorf("认证数据库不支持任务上报密钥")
		}
		return keys, nil
	}}
}

var jobIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// newJobKey makes a report key; only its digest is stored, so a lost key is replaced, never recovered.
func newJobKey(entropy io.Reader) (string, error) {
	var secret [32]byte
	defer clear(secret[:])
	if _, err := io.ReadFull(entropy, secret[:]); err != nil {
		return "", fmt.Errorf("生成上报密钥随机值失败：%w", err)
	}
	return api.JobKeyPrefix + base64.RawURLEncoding.EncodeToString(secret[:]), nil
}

type jobKeyOutput struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Job     string `json:"job,omitempty"`
	Key     string `json:"key,omitempty"`
}

func newJobCommand(deps jobKeyDependencies) *cobra.Command {
	root := &cobra.Command{Use: "job", Short: "批处理任务：生成业务事件上报密钥"}
	var asJSON bool
	key := &cobra.Command{Use: "key <任务ID>", Short: "以管理员身份为批处理任务生成上报密钥；旧密钥立即失效", Args: func(_ *cobra.Command, values []string) error {
		if len(values) != 1 || !jobIDPattern.MatchString(values[0]) {
			return &usageError{fmt.Errorf("请指定一个任务 ID：小写字母、数字或 -")}
		}
		return nil
	}, RunE: func(cmd *cobra.Command, values []string) error {
		result := jobKeyOutput{Status: "ok", Job: values[0], Message: "上报密钥只显示这一次。它只能给这个任务上报事件，不能读取或修改别的内容；放进任务的运行环境，例如 LGMON_JOB_KEY"}
		issue := func() error {
			if err := requireTokenAdmin(tokenDependencies{Administrator: deps.Administrator}); err != nil {
				return err
			}
			if deps.Entropy == nil {
				return fmt.Errorf("生成上报密钥需要安全随机源")
			}
			value, err := newJobKey(deps.Entropy)
			if err != nil {
				return err
			}
			home, _ := cmd.Flags().GetString("home")
			backend, err := deps.Open(cmd.Context(), home)
			if err != nil {
				return err
			}
			digest := api.TokenHash(value)
			if err := errors.Join(backend.SetJobKey(cmd.Context(), values[0], digest[:]), backend.Close()); err != nil {
				return err
			}
			result.Key = value
			return nil
		}
		failure := issue()
		if failure != nil {
			result.Status, result.Message = "error", failure.Error()
		}
		if asJSON {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
				return fmt.Errorf("输出上报密钥失败：%w", err)
			}
			if failure != nil {
				return &reportedError{failure}
			}
			return nil
		}
		if failure != nil {
			return failure
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n任务：%s\n%s\n上报：POST /api/v1/jobs/%s/events，请求头 Authorization: Bearer <密钥>\n", result.Message, result.Job, result.Key, result.Job)
		return err
	}}
	key.Flags().BoolVar(&asJSON, "json", false, "输出 JSON")
	root.AddCommand(key)
	return root
}
