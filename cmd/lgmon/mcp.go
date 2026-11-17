package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/LFenX/lg-monitor/internal/agents"
	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

type mcpRunner func(context.Context, *mcp.Server, mcp.Transport) error

func runMCP(ctx context.Context, server *mcp.Server, transport mcp.Transport) error {
	if err := server.Run(ctx, transport); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("MCP stdio 服务结束：%w", err)
	}
	return nil
}

type mcpOutput struct{ io.Writer }

func (mcpOutput) Close() error { return nil }

func newMCPCommand(run mcpRunner, token func() string, ancestors func(context.Context) []agents.Process) *cobra.Command {
	var address, tools string
	command := &cobra.Command{Use: "mcp", Short: "通过 stdio 提供 MCP 工具，使用 LGMON_TOKEN 访问监控器 HTTP API", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := api.NewMCPHTTPClient(api.MCPHTTPClientOptions{BaseURL: address, Token: token()})
			if err != nil {
				return err
			}
			if tools != "all" && tools != "essential" {
				return &usageError{fmt.Errorf("--tools 必须是 essential 或 all")}
			}
			presence := newMCPPresence(cmd.Context(), client, mcpPresenceInterval, ancestors)
			server, err := api.NewMCPServer(api.MCPOptions{Caller: client, Version: version, Logger: slog.Default(), Essential: tools == "essential", OnClient: presence.identify})
			if err != nil {
				return err
			}
			defer presence.close()
			var transport mcp.Transport
			if cmd.InOrStdin() == os.Stdin && cmd.OutOrStdout() == os.Stdout {
				transport = &mcp.StdioTransport{MaxLineLength: api.MaxValidationRequestBody + 1024}
			} else {
				reader, ok := cmd.InOrStdin().(io.ReadCloser)
				if !ok {
					reader = io.NopCloser(cmd.InOrStdin())
				}
				transport = &mcp.IOTransport{Reader: reader, Writer: mcpOutput{cmd.OutOrStdout()}, MaxLineLength: api.MaxValidationRequestBody + 1024}
			}
			return run(cmd.Context(), server, transport)
		}}
	command.Flags().StringVar(&address, "url", "http://127.0.0.1:7444", "监控器 HTTP/HTTPS 基础地址；令牌只从 LGMON_TOKEN 读取")
	command.Flags().StringVar(&tools, "tools", "all", "essential 只提供添加监控、做面板和排查告警的常用工具；all 提供全部工具")
	command.Flags().Bool("json", false, "显式使用既有 JSON-RPC stdio 协议，不增加外层封装")
	return command
}
