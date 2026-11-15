package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/spf13/cobra"
)

type connectRequest struct {
	Executable string `json:"executable"`
	Home       string `json:"home"`
	URL        string `json:"url"`
	Scope      string `json:"scope"`
	Name       string `json:"name"`
	Tools      string `json:"tools"`
}

type connectElevation interface {
	Connect(context.Context, connectRequest) (connectOutput, error)
}

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
	Tools      string         `json:"tools,omitempty"`
}

func quotedArgument(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

// newConnectCommand issues a token and prints ready-to-paste MCP settings for common agent clients.
func newConnectCommand(tokens tokenDependencies, agent agentDependencies, now func() time.Time) *cobra.Command {
	return newConnectCommandWithElevation(tokens, agent, now, nativeConnectElevation{})
}

func newConnectCommandWithElevation(tokens tokenDependencies, agent agentDependencies, now func() time.Time, elevation connectElevation) *cobra.Command {
	var scope, name, address, toolSet string
	var resultPipe string
	var asJSON, elevate bool
	command := &cobra.Command{Use: "connect", Short: "一步接入 Agent：创建令牌并给出各客户端可直接粘贴的 MCP 配置", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if resultPipe != "" {
			return serveConnectElevation(cmd.Context(), resultPipe, func(request connectRequest) connectOutput {
				return elevatedConnectResult(cmd.Context(), request, tokens, agent, now)
			})
		}
		result := connectOutput{Status: "ok", Message: "令牌只显示这一次；下面的命令和配置里已经包含它，请不要发到聊天、截图或提交到仓库"}
		var local agentContext
		permissionDenied := false
		work := func() error {
			permission := api.Scope(scope)
			if permission != api.ScopeRead && permission != api.ScopeConfig {
				return &usageError{fmt.Errorf("--scope 必须是 config（可添加监控和面板）或 read（只能查看）")}
			}
			if toolSet != "essential" && toolSet != "all" {
				return &usageError{fmt.Errorf("--tools 必须是 essential 或 all")}
			}
			home, _ := cmd.Flags().GetString("home")
			var err error
			local, err = agentLocalContext(agent, home)
			if err != nil {
				return err
			}
			if address == "" {
				settings, err := config.LoadSettings(os.DirFS(filepath.Join(local.Home, "config")))
				if err != nil {
					permissionDenied = errors.Is(err, os.ErrPermission)
					return fmt.Errorf("读取监控器设置失败，请用 --url 指定监控器地址：%w", err)
				}
				address = "http://" + settings.HTTPAddress()
			}
			if _, err := api.NewMCPHTTPClient(api.MCPHTTPClientOptions{BaseURL: address, Token: "check"}); err != nil {
				return &usageError{err}
			}
			if tokens.Administrator == nil {
				return fmt.Errorf("令牌管理需要管理员身份检查器")
			}
			allowed, err := tokens.Administrator()
			if err != nil {
				return fmt.Errorf("检查接入权限失败：%w", err)
			}
			if !allowed {
				permissionDenied = true
				return fmt.Errorf("数据目录 %s 的接入需要管理员权限；可加 --elevate 由 Windows 请求一次授权，或以管理员身份运行同一条命令", local.Home)
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
				permissionDenied = errors.Is(err, os.ErrPermission)
				return err
			}
			row, saveErr := backend.AddAPIToken(cmd.Context(), token.Digest[:], token.Name, string(token.Scope))
			if err := errors.Join(saveErr, backend.Close()); err != nil {
				return err
			}
			arguments := []string{"mcp", "--tools", toolSet, "--url", address}
			tail := quotedArgument(local.Executable) + " " + strings.Join(arguments, " ")
			result.Name, result.Scope, result.TokenID, result.Token = token.Name, token.Scope, row.ID, token.Value
			result.URL, result.HTTP, result.Tools = address, strings.TrimRight(address, "/")+"/mcp", toolSet
			if toolSet == "essential" {
				result.HTTP += "/essential"
			}
			result.Executable, result.Home = local.Executable, local.Home
			result.Claude = "claude mcp add lgmon --scope user --env LGMON_TOKEN=" + token.Value + " -- " + tail
			result.Codex = "codex mcp add lgmon --env LGMON_TOKEN=" + token.Value + " -- " + tail
			result.MCPServers = map[string]any{"lgmon": map[string]any{"command": local.Executable, "args": arguments, "env": map[string]string{"LGMON_TOKEN": token.Value}}}
			result.FirstWords = "调用 lgmon 的 start_here 和 list_boards；没有目标面板时用 create_board 直接创建，不要让我手动建。然后帮我看看这台电脑上有什么值得监控，把最重要的几个服务加进面板；附属的 Tunnel、网关、数据库用 parent 挂在主服务下面，最后把面板地址告诉我。"
			return nil
		}
		failure := work()
		if failure != nil && permissionDenied && elevate && elevation != nil {
			result, failure = elevation.Connect(cmd.Context(), connectRequest{Executable: local.Executable, Home: local.Home, URL: address, Scope: scope, Name: name, Tools: toolSet})
		}
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
	command.Flags().BoolVar(&elevate, "elevate", false, "权限不足时由 Windows 请求一次授权；取消后不重试，令牌仅在内存中返回")
	command.Flags().StringVar(&toolSet, "tools", "essential", "essential 为面板和监控常用工具；all 包含飞书接入等管理工具，权限仍由令牌 scope 决定")
	command.Flags().StringVar(&resultPipe, "result-pipe", "", "本机接入辅助进程的受保护返回管道")
	_ = command.Flags().MarkHidden("result-pipe")
	return command
}

func elevatedConnectResult(ctx context.Context, request connectRequest, tokens tokenDependencies, agent agentDependencies, now func() time.Time) connectOutput {
	executable, err := agent.Executable()
	if err != nil || !filepath.IsAbs(request.Home) || !strings.EqualFold(filepath.Clean(request.Executable), filepath.Clean(executable)) {
		return connectOutput{Status: "error", Message: "接入辅助进程的程序或数据目录不匹配"}
	}
	command := newConnectCommandWithElevation(tokens, agent, now, nil)
	command.Flags().String("home", request.Home, "接入数据目录")
	command.SetArgs([]string{"--scope", request.Scope, "--name", request.Name, "--url", request.URL, "--tools", request.Tools, "--json"})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(io.Discard)
	_ = command.ExecuteContext(ctx)
	var result connectOutput
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		return connectOutput{Status: "error", Message: "接入辅助进程未返回有效结果"}
	}
	return result
}
