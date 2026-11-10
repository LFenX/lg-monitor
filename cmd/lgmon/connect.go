package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/spf13/cobra"
)

type connectOutput struct {
	Status     string         `json:"status"`
	Message    string         `json:"message"`
	Name       string         `json:"name,omitempty"`
	Scope      api.Scope      `json:"scope,omitempty"`
	TokenID    string         `json:"tokenId,omitempty"`
	Token      string         `json:"token,omitempty"`
	URL        string         `json:"url,omitempty"`
	HTTP       string         `json:"http,omitempty"`
	Executable string         `json:"executable,omitempty"`
	Home       string         `json:"home,omitempty"`
	Claude     string         `json:"claude,omitempty"`
	Codex      string         `json:"codex,omitempty"`
	MCPServers map[string]any `json:"mcpServers,omitempty"`
	FirstWords string         `json:"firstWords,omitempty"`
}

func quotedArgument(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

// newConnectCommand issues a token and prints ready-to-paste MCP settings for common agent clients.
func newConnectCommand(tokens tokenDependencies, agent agentDependencies, now func() time.Time) *cobra.Command {
	var scope, name, address string
	var asJSON bool
	command := &cobra.Command{Use: "connect", Short: "一步接入 Agent：创建令牌并给出各客户端可直接粘贴的 MCP 配置", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		result := connectOutput{Status: "ok", Message: "令牌只显示这一次；下面的命令和配置里已经包含它，请不要发到聊天、截图或提交到仓库"}
		work := func() error {
			permission := api.Scope(scope)
			if permission != api.ScopeRead && permission != api.ScopeConfig {
				return &usageError{fmt.Errorf("--scope 必须是 config（可添加监控和面板）或 read（只能查看）")}
			}
			home, _ := cmd.Flags().GetString("home")
			local, err := agentLocalContext(agent, home)
			if err != nil {
				return err
			}
			if address == "" {
				settings, err := config.LoadSettings(os.DirFS(filepath.Join(local.Home, "config")))
				if err != nil {
					return fmt.Errorf("读取监控器设置失败，请用 --url 指定监控器地址：%w", err)
				}
				address = "http://" + settings.HTTPAddress()
			}
			if _, err := api.NewMCPHTTPClient(api.MCPHTTPClientOptions{BaseURL: address, Token: "check"}); err != nil {
				return &usageError{err}
			}
			if err := requireTokenAdmin(tokens); err != nil {
				return fmt.Errorf("数据目录 %s 属于系统服务，创建令牌需要管理员权限。请右键“以管理员身份运行”PowerShell，再执行同一条命令：%w", local.Home, err)
			}
			if name == "" {
				name = "agent-" + now().Format("0102-1504")
			}
			token, err := api.NewToken(permission, name, tokens.Entropy)
			if err != nil {
				return err
			}
			backend, err := tokens.Open(cmd.Context(), local.Home)
			if err != nil {
				return err
			}
			row, saveErr := backend.AddAPIToken(cmd.Context(), token.Digest[:], token.Name, string(token.Scope))
			if err := errors.Join(saveErr, backend.Close()); err != nil {
				return err
			}
			arguments := []string{"mcp", "--tools", "essential", "--url", address}
			tail := quotedArgument(local.Executable) + " " + strings.Join(arguments, " ")
			result.Name, result.Scope, result.TokenID, result.Token = token.Name, token.Scope, row.ID, token.Value
			result.URL, result.HTTP = address, strings.TrimRight(address, "/")+"/mcp/essential"
			result.Executable, result.Home = local.Executable, local.Home
			result.Claude = "claude mcp add lg-monitor --scope user --env LGMON_TOKEN=" + token.Value + " -- " + tail
			result.Codex = "codex mcp add lg-monitor --env LGMON_TOKEN=" + token.Value + " -- " + tail
			result.MCPServers = map[string]any{"lg-monitor": map[string]any{"command": local.Executable, "args": arguments, "env": map[string]string{"LGMON_TOKEN": token.Value}}}
			result.FirstWords = "调用 lg-monitor 的 start_here，然后帮我看看这台电脑上有什么值得监控，把最重要的几个加上监控并做一页总览面板。"
			return nil
		}
		failure := work()
		if failure != nil {
			result = connectOutput{Status: "error", Message: failure.Error()}
		}
		if asJSON {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
				return fmt.Errorf("输出接入结果失败：%w", err)
			}
			if failure != nil {
				return &reportedError{failure}
			}
			return nil
		}
		if failure != nil {
			return failure
		}
		servers, err := json.MarshalIndent(map[string]any{"mcpServers": result.MCPServers}, "", "  ")
		if err != nil {
			return fmt.Errorf("编码 MCP 配置失败：%w", err)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), `已为 Agent 创建访问令牌 %s（权限 %s）。
%s。

选你在用的 Agent，照做其中一项即可：

[1] Claude Code —— 在终端运行这一条命令：

%s

[2] Codex CLI —— 在终端运行这一条命令：

%s

[3] Cursor、Cline、Windsurf、Gemini CLI 等 —— 把下面这段合并进它的 MCP 配置文件：

%s

[4] 只支持 HTTP 的客户端 —— 地址 %s
    请求头 Authorization: Bearer %s

接好以后，对 Agent 说这句话就能开始：

  %s

监控面板：%s
撤销这个令牌：lgmon token revoke %s --home %s
`, result.Name, result.Scope, result.Message, result.Claude, result.Codex, servers, result.HTTP, result.Token, result.FirstWords, result.URL, result.TokenID, quotedArgument(result.Home))
		return err
	}}
	command.Flags().StringVar(&scope, "scope", "config", "config 可添加监控和面板；read 只能查看")
	command.Flags().StringVar(&name, "name", "", "令牌名称，默认 agent-月日-时分")
	command.Flags().StringVar(&address, "url", "", "监控器 HTTP 地址；默认读取 home 里的设置，通常是 http://127.0.0.1:7444")
	command.Flags().BoolVar(&asJSON, "json", false, "以 JSON 输出令牌和各客户端配置")
	return command
}
