package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/winsvc"
	"github.com/spf13/cobra"
)

type setupDependencies struct {
	Factory       func(context.Context) (*winsvc.Setup, error)
	Administrator func() (bool, error)
	Paths         secrets.PathPolicy
	Guard         func(string) error
	Panel         func(string) (string, error)
}

func defaultSetupDependencies() setupDependencies { return platformSetupDependencies() }

func printSetupResult(cmd *cobra.Command, asJSON bool, value any, failure error) error {
	if failure != nil {
		if !asJSON {
			return failure
		}
		value = map[string]any{"error": map[string]string{"code": "setup_failed", "message": failure.Error()}}
	}
	if asJSON {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(value); err != nil {
			return fmt.Errorf("输出安装结果失败：%w", err)
		}
	} else {
		var message string
		switch result := value.(type) {
		case winsvc.SetupPlan:
			message = result.Message
		case winsvc.SetupResult:
			message = result.Message
		case string:
			message = result
		}
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), message); err != nil {
			return fmt.Errorf("输出安装结果失败：%w", err)
		}
	}
	if failure != nil {
		return &reportedError{failure}
	}
	return nil
}

func newSetupCommand(deps setupDependencies) *cobra.Command {
	root := &cobra.Command{Use: "setup", Short: "安装包使用的本机初始化、安全配对和受保护服务编排"}
	var asJSON bool
	root.PersistentFlags().BoolVar(&asJSON, "json", false, "输出 JSON，不输出配对码或凭据")
	admin := func() error {
		if deps.Administrator == nil {
			return fmt.Errorf("安装管理员身份检查未设置")
		}
		allowed, err := deps.Administrator()
		if err != nil {
			return fmt.Errorf("检查安装管理员身份失败：%w", err)
		}
		if !allowed {
			return fmt.Errorf("本机安装操作需要管理员终端或安装器的 UAC 确认")
		}
		return nil
	}
	for _, use := range []string{"check", "install", "remove"} {
		var role, listen, monitor, pairPath string
		var confirmed, pairStdin, serviceAbsent, requirePaired, panelURL bool
		command := &cobra.Command{Use: use, Short: map[string]string{"check": "只读检查现有服务、程序与配置，不创建目录或注册服务", "install": "明确确认后初始化缺失配置、配对、注册并启动服务", "remove": "明确确认并核对程序/home 后移除本安装服务，保留全部数据"}[use], Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			run := func() (any, error) {
				if err := admin(); err != nil {
					return nil, err
				}
				if use != "check" && !confirmed {
					return nil, fmt.Errorf("需要 --confirm 明确同意本次服务安装/启动或卸载；不接受默认同意")
				}
				home, _ := cmd.Flags().GetString("home")
				home, err := filepath.Abs(home)
				if err != nil {
					return nil, fmt.Errorf("解析安装 home 失败：%w", err)
				}
				if deps.Guard != nil {
					if err := deps.Guard(home); err != nil {
						return nil, err
					}
				}
				home, err = setupCanonical(home)
				if err != nil {
					return nil, fmt.Errorf("解析安装 home 实际路径失败：%w", err)
				}
				if use == "check" && panelURL {
					if serviceAbsent || requirePaired || deps.Panel == nil {
						return nil, fmt.Errorf("面板地址查询不能混用其他检查模式")
					}
					return deps.Panel(home)
				}
				if deps.Factory == nil {
					return nil, fmt.Errorf("安装服务编排未设置")
				}
				installer, err := deps.Factory(cmd.Context())
				if err != nil {
					return nil, err
				}
				if use == "check" && serviceAbsent {
					return "已确认 lgmon 服务不存在，尚未修改文件或注册服务", installer.VerifyAbsent(cmd.Context())
				}
				if use == "remove" {
					return installer.Remove(cmd.Context(), home, confirmed)
				}
				request := winsvc.SetupRequest{Home: home, Role: role, Listen: listen, Monitor: monitor, Confirmed: confirmed}
				if role == "collector" && !cmd.Flags().Changed("listen") {
					request.Listen = ""
				}
				if use == "install" {
					if pairPath != "" && pairStdin || role != "collector" && (pairPath != "" || pairStdin) {
						return nil, fmt.Errorf("配对输入仅供 collector，--pair-input 和 --pair-stdin 不能同时使用")
					}
					var pair setupPairInput
					if pairPath != "" {
						pair, err = readSetupPairFile(pairPath, deps.Paths, deps.Guard)
					} else if pairStdin {
						pair, err = readSetupPairInput(cmd.InOrStdin())
					}
					if err != nil {
						return nil, err
					}
					request.Code, request.Fingerprint = pair.Code, pair.Fingerprint
					return installer.Install(cmd.Context(), request)
				}
				plan, err := installer.Check(cmd.Context(), request)
				if err == nil && requirePaired && plan.PairingRequired {
					err = fmt.Errorf("该数据目录尚未配对，请填写首次配对码和已核对指纹，不能选择复用身份")
				}
				return plan, err
			}
			value, err := run()
			return printSetupResult(cmd, asJSON, value, err)
		}}
		if use != "remove" {
			command.Flags().StringVar(&role, "role", "both", "安装角色：collector、monitor 或 both")
			command.Flags().StringVar(&listen, "listen", "127.0.0.1:7443", "monitor/both 的 gRPC 监听地址，不修改防火墙")
			command.Flags().StringVar(&monitor, "monitor", "", "collector 连接的监控器 host:port")
		}
		if use != "check" {
			command.Flags().BoolVar(&confirmed, "confirm", false, "本人已核对角色、目录和本次服务影响，明确同意")
		}
		if use == "install" {
			command.Flags().StringVar(&pairPath, "pair-input", "", "受保护的一次性 code/fingerprint JSON 文件，读取后删除")
			command.Flags().BoolVar(&pairStdin, "pair-stdin", false, "从 stdin 读取 code/fingerprint JSON，不在参数中传递秘密")
		}
		if use == "check" {
			command.Flags().BoolVar(&serviceAbsent, "service-absent", false, "仅只读核对没有 lgmon 服务，卸载删除文件前使用")
			command.Flags().BoolVar(&requirePaired, "require-paired", false, "要求已存在属于该监控器的有效本机配对身份")
			command.Flags().BoolVar(&panelURL, "panel-url", false, "只读返回当前配置对应的本机面板地址")
		}
		root.AddCommand(command)
	}
	var inputDirectory string
	inbox := &cobra.Command{Use: "inbox", Short: "创建新的受保护输入目录和空 pair.json，拒绝接管既有目录", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		err := admin()
		if err == nil {
			if deps.Paths == nil || deps.Guard == nil {
				err = fmt.Errorf("安装输入路径保护未设置")
			} else {
				err = setupInbox(inputDirectory, deps.Paths, deps.Guard)
			}
		}
		return printSetupResult(cmd, asJSON, "安装输入目录已创建并保护，尚未注册或启动服务", err)
	}}
	inbox.Flags().StringVar(&inputDirectory, "directory", "", "新的临时输入目录绝对路径")
	_ = inbox.MarkFlagRequired("directory")
	root.AddCommand(inbox)
	return root
}
