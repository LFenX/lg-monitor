package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/transport"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"github.com/spf13/cobra"
)

type pairDependencies struct {
	Admin func(context.Context, string) (lgmonv1.PairingClient, func() error, error)
	Join  func(context.Context, string, string, string, string, func(string) (bool, error)) (string, error)
}

func defaultPairDependencies() pairDependencies {
	return pairDependencies{
		Admin: func(ctx context.Context, home string) (lgmonv1.PairingClient, func() error, error) {
			vault, err := secrets.New(home, slog.Default())
			if err != nil {
				return nil, nil, err
			}
			connection, err := transport.ConnectAdmin(ctx, transport.FileState{Home: home}, vault, time.Now, nil)
			if err != nil {
				return nil, nil, err
			}
			return lgmonv1.NewPairingClient(connection), connection.Close, nil
		},
		Join: func(ctx context.Context, home, address, code, fingerprint string, confirm func(string) (bool, error)) (string, error) {
			vault, err := secrets.New(home, slog.Default())
			if err != nil {
				return "", err
			}
			id, err := (collector.FileIDStore{Home: home}).LoadOrCreate()
			if err != nil {
				return "", err
			}
			hostname, err := os.Hostname()
			if err != nil {
				return "", fmt.Errorf("读取采集器主机名失败：%w", err)
			}
			return transport.Join(ctx, transport.JoinOptions{Address: address, CollectorID: id, Hostname: hostname, Code: code, Fingerprint: fingerprint, Keys: vault, State: transport.FileState{Home: home}, Confirm: confirm})
		},
	}
}

type pairOutput struct {
	Status           string                  `json:"status"`
	Message          string                  `json:"message"`
	Code             string                  `json:"code,omitempty"`
	ExpiresAt        int64                   `json:"expiresAt,omitempty"`
	Fingerprint      string                  `json:"fingerprint,omitempty"`
	FingerprintShort string                  `json:"fingerprintShort,omitempty"`
	CollectorID      string                  `json:"collectorId,omitempty"`
	Collectors       *[]transport.Credential `json:"collectors,omitempty"`
}

func pairResult(cmd *cobra.Command, asJSON bool, result pairOutput, failure error) error {
	if failure != nil {
		result.Status = "error"
		result.Message = failure.Error()
	}
	result.FingerprintShort = transport.FingerprintShort(result.Fingerprint)
	if asJSON {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
			return fmt.Errorf("输出配对结果失败：%w", err)
		}
		if failure != nil {
			return &reportedError{failure}
		}
		return nil
	}
	if failure != nil {
		return failure
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), result.Message); err != nil {
		return err
	}
	if result.Code != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "配对码：%s\n过期时间：%s\n", result.Code, time.UnixMilli(result.ExpiresAt).Local().Format(time.RFC3339))
	}
	if result.Fingerprint != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "CA 指纹（前 16 位）：%s\n完整指纹：%s\n", result.FingerprintShort, result.Fingerprint)
	}
	if result.Collectors != nil {
		for _, item := range *result.Collectors {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t已吊销=%t\n", item.ID, item.Hostname, item.Revoked)
		}
	}
	return nil
}

func newPairCommand(deps pairDependencies) *cobra.Command {
	var asJSON bool
	root := &cobra.Command{Use: "pair", Short: "生成一次性码并通过 CA 指纹配对采集器", Args: noArgs}
	root.PersistentFlags().BoolVar(&asJSON, "json", false, "输出稳定 JSON")
	var ttlText string
	create := &cobra.Command{Use: "new", Short: "在运行中的监控器生成一次性配对码", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		ttl, err := config.Duration(ttlText).Parse()
		if err != nil || ttl < time.Second || ttl > 24*time.Hour {
			return &usageError{fmt.Errorf("--ttl 必须为 1s～24h")}
		}
		home, err := cmd.Flags().GetString("home")
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
		defer cancel()
		client, close, err := deps.Admin(ctx, home)
		if err != nil {
			return pairResult(cmd, asJSON, pairOutput{}, err)
		}
		defer close()
		response, err := client.NewCode(ctx, &lgmonv1.NewCodeRequest{TtlMillis: ttl.Milliseconds()})
		if err != nil {
			return pairResult(cmd, asJSON, pairOutput{}, fmt.Errorf("生成配对码失败：%w", err))
		}
		return pairResult(cmd, asJSON, pairOutput{Status: "ok", Message: "请在采集器核对 CA 指纹后使用此码；配对码仅能使用一次", Code: response.Code, ExpiresAt: response.ExpiresAt, Fingerprint: response.Fingerprint}, nil)
	}}
	create.Flags().StringVar(&ttlText, "ttl", "10m", "配对码有效期（1s～24h，可写 1d）")
	var address, code, fingerprint string
	join := &cobra.Command{Use: "join", Short: "核对 CA 指纹后申请采集器证书", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		home, err := cmd.Flags().GetString("home")
		if err != nil {
			return err
		}
		var confirm func(string) (bool, error)
		if !asJSON {
			confirm = func(actual string) (bool, error) {
				fmt.Fprintf(cmd.OutOrStdout(), "收到的 CA 短指纹：%s\n完整指纹：%s\n请与监控器核对，输入 16 位短指纹或完整指纹确认：", transport.FingerprintShort(actual), actual)
				line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if err != nil && len(line) == 0 {
					return false, fmt.Errorf("读取指纹确认失败：%w", err)
				}
				if err := transport.VerifyFingerprint(actual, line); err != nil {
					return false, err
				}
				return true, nil
			}
		}
		actual, err := deps.Join(cmd.Context(), home, address, code, fingerprint, confirm)
		var required *transport.FingerprintRequired
		if errors.As(err, &required) {
			return pairResult(cmd, asJSON, pairOutput{Status: "action_required", Message: required.Error(), Fingerprint: required.Fingerprint}, nil)
		}
		if err != nil {
			return pairResult(cmd, asJSON, pairOutput{Fingerprint: actual}, err)
		}
		return pairResult(cmd, asJSON, pairOutput{Status: "ok", Message: "采集器已配对，后续连接使用 mTLS 并校验 CA", Fingerprint: actual}, nil)
	}}
	join.Flags().StringVar(&address, "monitor", "", "监控器的主机:端口")
	join.Flags().StringVar(&code, "code", "", "一次性配对码")
	join.Flags().StringVar(&fingerprint, "fingerprint", "", "已核对的 CA 指纹（16 位短指纹或完整 64 位）")
	join.MarkFlagRequired("monitor")
	join.MarkFlagRequired("code")
	root.AddCommand(create, join)
	return root
}

func newCollectorsCommand(deps pairDependencies) *cobra.Command {
	var asJSON bool
	root := &cobra.Command{Use: "collectors", Short: "管理已配对采集器", Args: noArgs}
	root.PersistentFlags().BoolVar(&asJSON, "json", false, "输出稳定 JSON")
	list := &cobra.Command{Use: "list", Short: "列出配对身份与吊销状态", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		home, err := cmd.Flags().GetString("home")
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
		defer cancel()
		client, close, err := deps.Admin(ctx, home)
		if err != nil {
			return pairResult(cmd, asJSON, pairOutput{}, err)
		}
		defer close()
		response, err := client.ListCollectors(ctx, &lgmonv1.ListCollectorsRequest{})
		if err != nil {
			return pairResult(cmd, asJSON, pairOutput{}, fmt.Errorf("查询采集器失败：%w", err))
		}
		items := make([]transport.Credential, 0, len(response.Collectors))
		result := pairOutput{Status: "ok", Message: fmt.Sprintf("已登记 %d 个采集器", len(response.Collectors)), Collectors: &items}
		for _, item := range response.Collectors {
			state := transport.AwaitingCollectorConfig()
			if item.Config != nil {
				state = transport.CollectorConfigState{State: item.Config.State, Version: item.Config.Version, Message: item.Config.Message}
			}
			items = append(items, transport.Credential{ID: item.Id, Hostname: item.Hostname, Serial: item.Serial, IssuedAt: item.IssuedAt, Revoked: item.Revoked, Config: &state})
		}
		return pairResult(cmd, asJSON, result, nil)
	}}
	revoke := &cobra.Command{Use: "revoke <id>", Short: "吊销证书并立即断开采集流", Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(1)(cmd, args); err != nil {
			return &usageError{err}
		}
		return nil
	}, RunE: func(cmd *cobra.Command, args []string) error {
		home, err := cmd.Flags().GetString("home")
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
		defer cancel()
		client, close, err := deps.Admin(ctx, home)
		if err != nil {
			return pairResult(cmd, asJSON, pairOutput{}, err)
		}
		defer close()
		if _, err := client.RevokeCollector(ctx, &lgmonv1.RevokeCollectorRequest{Id: args[0]}); err != nil {
			return pairResult(cmd, asJSON, pairOutput{}, fmt.Errorf("吊销采集器失败：%w", err))
		}
		return pairResult(cmd, asJSON, pairOutput{Status: "ok", Message: "证书已吊销，现有采集流已取消，后续连接将被拒绝", CollectorID: args[0]}, nil)
	}}
	root.AddCommand(list, revoke)
	return root
}
