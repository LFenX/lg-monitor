package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/spf13/cobra"
)

type importDependencies struct {
	Token  func() string
	Client func(string, string) (api.MCPCaller, error)
	Wait   func(context.Context, time.Duration) error
}

func defaultImportDependencies() importDependencies {
	return importDependencies{Token: func() string { return os.Getenv("LGMON_TOKEN") }, Client: func(address, token string) (api.MCPCaller, error) {
		return api.NewMCPHTTPClient(api.MCPHTTPClientOptions{BaseURL: address, Token: token})
	}, Wait: func(ctx context.Context, delay time.Duration) error {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}}
}

func newImportCommand(deps importDependencies) *cobra.Command {
	root := &cobra.Command{Use: "import", Short: "从已有服务配置预览并导入监控目标", Args: noArgs}
	var address, collector, refs string
	var public, apply, remove, yes, asJSON bool
	command := &cobra.Command{Use: "service-switch", Short: "从采集器本机固定 services.json 导入，只生成目标，不生成规则", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if collector == "" {
				return &usageError{fmt.Errorf("请用 --collector 指定采集器 ID 或主机名")}
			}
			if apply && asJSON && !yes {
				return &usageError{fmt.Errorf("--json --apply 必须同时提供 --yes，确认允许保存预览中的变化")}
			}
			client, err := deps.Client(address, deps.Token())
			if err != nil {
				return err
			}
			call := func(op api.Operation, request api.Request, value any) error {
				response, err := client.Call(cmd.Context(), op, request)
				if err != nil {
					return err
				}
				if response.Status < 200 || response.Status >= 300 {
					var failure struct {
						Error struct {
							Code    string `json:"code"`
							Message string `json:"message"`
						} `json:"error"`
					}
					if json.Unmarshal(response.Body, &failure) == nil && failure.Error.Code != "" {
						return fmt.Errorf("%s：%s", failure.Error.Code, failure.Error.Message)
					}
					return fmt.Errorf("监控器返回 HTTP %d", response.Status)
				}
				if err := json.Unmarshal(response.Body, value); err != nil {
					return fmt.Errorf("解析监控器导入结果失败：%w", err)
				}
				return nil
			}
			var hosts []api.Collector
			if err := call(api.ListCollectors, api.Request{}, &hosts); err != nil {
				return err
			}
			id := ""
			for _, host := range hosts {
				if host.ID == collector {
					id = host.ID
					break
				}
			}
			if id == "" {
				for _, host := range hosts {
					if strings.EqualFold(host.Hostname, collector) {
						if id != "" {
							return fmt.Errorf("主机名对应多个采集器，请改用采集器 ID")
						}
						id = host.ID
					}
				}
			}
			if id == "" {
				return fmt.Errorf("采集器不存在，请先运行 lgmon collectors list")
			}
			var preview config.ServiceSwitchPreview
			request := api.Request{Params: map[string]string{"id": id}, Query: url.Values{"publicProbes": {fmt.Sprint(public)}}}
			if err := call(api.PreviewServiceSwitch, request, &preview); err != nil {
				return err
			}
			if !asJSON {
				fmt.Fprintf(cmd.OutOrStdout(), "主机：%s (%s)\n", preview.Collector.Hostname, preview.Collector.ID)
				for _, item := range preview.Items {
					fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s", item.Action, item.Ref, item.TargetID, item.Name)
					if item.Autostart == "off" {
						fmt.Fprint(cmd.OutOrStdout(), "（开机不自动启动）")
					}
					if item.Reason != nil {
						fmt.Fprint(cmd.OutOrStdout(), "："+*item.Reason)
					}
					fmt.Fprintln(cmd.OutOrStdout())
					if item.Target != nil {
						data, _ := json.MarshalIndent(item.Target, "", "  ")
						fmt.Fprintln(cmd.OutOrStdout(), string(data))
					}
				}
			}
			if !apply {
				if asJSON {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(preview)
				}
				return nil
			}
			input := map[string]any{"sha256": preview.SHA256, "targetsVersion": preview.TargetsVersion, "removeMissing": remove, "publicProbes": public}
			if cmd.Flags().Changed("refs") {
				selected := []string{}
				if strings.TrimSpace(refs) != "" {
					for _, ref := range strings.Split(refs, ",") {
						selected = append(selected, strings.TrimSpace(ref))
					}
				}
				input["refs"] = selected
			}
			if !yes {
				fmt.Fprintf(cmd.OutOrStdout(), "所选 refs：%v；删除缺失目标：%t。只保存目标，不生成规则。确认导入？[y/N] ", input["refs"], remove)
				scanner := bufio.NewScanner(cmd.InOrStdin())
				if !scanner.Scan() || (strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" && strings.ToLower(strings.TrimSpace(scanner.Text())) != "yes") {
					if err := scanner.Err(); err != nil {
						return fmt.Errorf("读取导入确认失败：%w", err)
					}
					fmt.Fprintln(cmd.OutOrStdout(), "已取消，未保存配置")
					return nil
				}
			}
			if err := deps.Wait(cmd.Context(), 2*time.Second); err != nil {
				return fmt.Errorf("等待导入保存已取消：%w", err)
			}
			body, err := json.Marshal(input)
			if err != nil {
				return fmt.Errorf("编码导入请求失败：%w", err)
			}
			var result config.ServiceSwitchImportResult
			if err := call(api.ImportServiceSwitch, api.Request{Params: request.Params, Body: body}, &result); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "已创建 %v，更新 %v，删除 %v。请再按需要添加告警规则。\n", result.Created, result.Updated, result.Removed)
			return err
		}}
	command.Flags().StringVar(&collector, "collector", "", "采集器 ID 或唯一主机名")
	command.Flags().StringVar(&address, "url", "http://127.0.0.1:7444", "监控器 HTTP/HTTPS 基础地址；令牌只从 LGMON_TOKEN 读取")
	command.Flags().BoolVar(&public, "public-probes", true, "生成公网域名探测")
	command.Flags().BoolVar(&apply, "apply", false, "预览并确认后保存")
	command.Flags().StringVar(&refs, "refs", "", "只保存逗号分隔的 create/update ref；省略为全部，空串为不保存任何 create/update")
	command.Flags().BoolVar(&remove, "remove-missing", false, "同时删除预览中的缺失目标")
	command.Flags().BoolVar(&yes, "yes", false, "确认导入预览中的变化")
	command.Flags().BoolVar(&asJSON, "json", false, "输出 JSON；保存时必须同时使用 --yes")
	root.AddCommand(command)
	return root
}
