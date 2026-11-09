package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/winsvc"
	"github.com/spf13/cobra"
)

type operationHTTPFactory func(string) (api.MCPCaller, error)

func printOperation(cmd *cobra.Command, value any, err error) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	if err != nil {
		if !jsonOutput {
			return err
		}
		value = map[string]any{"error": map[string]string{"code": "operation_failed", "message": err.Error()}}
	}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	if !jsonOutput {
		encoder.SetIndent("", "  ")
	}
	if writeErr := encoder.Encode(value); writeErr != nil {
		return fmt.Errorf("输出服务操作结果失败：%w", writeErr)
	}
	if err != nil {
		return &reportedError{err}
	}
	return nil
}
func newOperationsCommand(administrator func() (bool, error), factory operationHTTPFactory) *cobra.Command {
	if administrator == nil {
		administrator = winsvc.IsAdministrator
	}
	root := &cobra.Command{Use: "operations", Short: "本机逐条授权及确认后重启采集器的精确 Windows 服务"}
	root.PersistentFlags().Bool("json", false, "输出 JSON")
	var address string
	root.PersistentFlags().StringVar(&address, "url", "http://127.0.0.1:7444", "远程查询/执行使用的监控器地址；令牌仅从 LGMON_TOKEN 读取")
	for _, operation := range []string{"status", "enable", "disable", "allow", "revoke"} {
		var service string
		command := &cobra.Command{Use: operation, Short: map[string]string{"status": "只读查看本机授权配置，不能代替在线执行环境检查", "enable": "本机管理员启用已经逐条登记的服务授权", "disable": "本机管理员关闭全部远程服务操作", "allow": "本机管理员登记一个精确 SCM 服务重启授权", "revoke": "本机管理员撤销指定授权"}[operation], Args: teamArgs(0, 0), RunE: func(cmd *cobra.Command, args []string) error {
			run := func() (any, error) {
				if operation != "status" {
					admin, err := administrator()
					if err != nil {
						return nil, fmt.Errorf("检查本机操作授权管理员身份失败：%w", err)
					}
					if !admin {
						return nil, fmt.Errorf("修改本机服务操作授权需要以管理员身份运行终端")
					}
				}
				home, _ := cmd.Flags().GetString("home")
				directory := filepath.Join(home, "config")
				manager, err := config.NewManager(config.FSLoader{FS: os.DirFS(directory)}, slog.Default())
				if err != nil {
					return nil, err
				}
				if operation != "status" {
					editor, err := config.NewEditor(config.DirectoryDocuments{Path: directory}, manager)
					if err != nil {
						return nil, err
					}
					if _, err := editor.Update(cmd.Context(), "settings.yaml", func(data []byte) ([]byte, error) {
						settings, err := config.ParseSettings(data)
						if err != nil {
							return nil, err
						}
						policy := settings.RemoteOperations
						switch operation {
						case "enable":
							policy.Enabled = true
						case "disable":
							policy.Enabled = false
						case "allow":
							grant := config.ServiceGrant{ID: args[0], Service: service}
							index := slices.IndexFunc(policy.Services, func(item config.ServiceGrant) bool { return item.ID == grant.ID })
							if index < 0 {
								policy.Services = append(policy.Services, grant)
							} else {
								policy.Services[index] = grant
							}
						case "revoke":
							policy.Services = slices.DeleteFunc(policy.Services, func(item config.ServiceGrant) bool { return item.ID == args[0] })
							if len(policy.Services) == 0 {
								policy.Enabled = false
							}
						}
						return config.RemoteOperationsConfig(data, policy)
					}); err != nil {
						return nil, err
					}
				}
				policy := manager.Snapshot().Settings.RemoteOperations
				grants := policy.Services
				if grants == nil {
					grants = []config.ServiceGrant{}
				}
				return map[string]any{"enabled": policy.Enabled, "services": grants, "version": policy.Version(), "scope": "local_config", "message": "仅显示本机配置；实际 LocalSystem 身份和路径检查结果请从在线 list 查看。没有安装或控制服务。"}, nil
			}
			value, err := run()
			return printOperation(cmd, value, err)
		}}
		if operation == "allow" || operation == "revoke" {
			command.Use += " <授权ID>"
			command.Args = teamArgs(1, 1)
		}
		if operation == "allow" {
			command.Flags().StringVar(&service, "service", "", "精确 SCM 服务名，不是显示名称或命令")
			_ = command.MarkFlagRequired("service")
		}
		root.AddCommand(command)
	}
	call := func(cmd *cobra.Command, operation api.Operation, params map[string]string, body any) error {
		if factory == nil {
			return printOperation(cmd, nil, fmt.Errorf("监控器 HTTP 调用器未配置"))
		}
		client, err := factory(address)
		if err != nil {
			return printOperation(cmd, nil, err)
		}
		var data []byte
		if body != nil {
			data, err = json.Marshal(body)
			if err != nil {
				return printOperation(cmd, nil, fmt.Errorf("编码操作请求失败：%w", err))
			}
		}
		response, err := client.Call(cmd.Context(), operation, api.Request{Params: params, Body: data})
		if err != nil {
			return printOperation(cmd, nil, err)
		}
		if response.Status >= 400 {
			asJSON, _ := cmd.Flags().GetBool("json")
			if asJSON {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(response.Body); err != nil {
					return fmt.Errorf("输出操作错误失败：%w", err)
				}
				return &reportedError{fmt.Errorf("操作未确认成功，请查询原请求记录")}
			}
			var failure struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(response.Body, &failure) != nil || failure.Error.Message == "" {
				return fmt.Errorf("监控器操作请求失败，请查询原请求记录")
			}
			return fmt.Errorf("%s", failure.Error.Message)
		}
		return printOperation(cmd, response.Body, nil)
	}
	var listCollector string
	list := &cobra.Command{Use: "list", Short: "只读列出指定在线采集器的逐条授权与策略版本", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if listCollector == "" {
			return printOperation(cmd, nil, fmt.Errorf("请用 --collector 指定采集器"))
		}
		return call(cmd, api.ListServiceOperations, map[string]string{"id": listCollector}, nil)
	}}
	list.Flags().StringVar(&listCollector, "collector", "", "在线采集器 ID")
	root.AddCommand(list)
	root.AddCommand(&cobra.Command{Use: "result <请求ID>", Short: "只读查询原操作结果，不重新执行", Args: teamArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
		return call(cmd, api.GetServiceOperation, map[string]string{"id": args[0]}, nil)
	}})
	var host, requestID, version, reason string
	var confirmed bool
	run := &cobra.Command{Use: "run <授权ID>", Short: "管理员逐次确认后执行服务重启；未知结果不得自动换 ID 重试", Args: teamArgs(1, 1), RunE: func(cmd *cobra.Command, args []string) error {
		if host == "" || !collector.ValidOperationRequestID(requestID) || !collector.ValidOperationVersion(version) || !confirmed || strings.TrimSpace(reason) == "" {
			return printOperation(cmd, nil, fmt.Errorf("必须指定 --collector、--request-id（32 位小写十六进制）、已查看的 --version、--reason 和 --confirm"))
		}
		return call(cmd, api.RestartService, map[string]string{"id": host}, api.ServiceOperationInput{RequestID: requestID, GrantID: args[0], Version: version, Reason: reason, Confirmed: true})
	}}
	run.Flags().StringVar(&host, "collector", "", "在线采集器 ID")
	run.Flags().StringVar(&requestID, "request-id", "", "本人本次确认的固定请求 ID；同一请求重试必须保留此 ID")
	run.Flags().StringVar(&version, "version", "", "list 返回、本人核对过的本机策略版本")
	run.Flags().StringVar(&reason, "reason", "", "本人填写的 1～200 字操作原因")
	run.Flags().BoolVar(&confirmed, "confirm", false, "已核对主机和精确服务，明确确认本次重启会中断服务")
	root.AddCommand(run)
	return root
}
