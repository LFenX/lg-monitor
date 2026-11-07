package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const mcpResponseLimit = 16 * 1024 * 1024

type MCPHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type MCPHTTPClientOptions struct {
	BaseURL string
	Token   string
	Client  MCPHTTPDoer
}

type MCPHTTPClient struct {
	base   *url.URL
	token  string
	client MCPHTTPDoer
}

func NewMCPHTTPClient(opts MCPHTTPClientOptions) (*MCPHTTPClient, error) {
	base, err := url.Parse(opts.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" || base.User != nil ||
		(base.Path != "" && base.Path != "/") || base.RawQuery != "" || base.Fragment != "" || base.Opaque != "" {
		return nil, fmt.Errorf("监控器地址必须是完整 HTTP/HTTPS 地址，不能包含路径、凭据、查询参数或片段")
	}
	if opts.Token == "" || len(opts.Token) > 512 || strings.ContainsAny(opts.Token, " \t\r\n") {
		return nil, fmt.Errorf("请通过 LGMON_TOKEN 提供有效的 HTTP API 令牌；不要把令牌放在命令行参数里")
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 95 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &MCPHTTPClient{base: base, token: opts.Token, client: opts.Client}, nil
}

func (c *MCPHTTPClient) Call(ctx context.Context, op Operation, input Request) (MCPResponse, error) {
	request, err := mcpAPIRequest(ctx, c.base, op, input)
	if err != nil {
		return MCPResponse{}, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return MCPResponse{}, fmt.Errorf("调用监控器已取消或超时：%w", ctx.Err())
		}
		return MCPResponse{}, fmt.Errorf("连接监控器失败，请检查地址、网络和监控器是否运行")
	}
	defer response.Body.Close()
	return readMCPResponse(response.StatusCode, response.Body)
}

func mcpAPIRequest(ctx context.Context, base *url.URL, op Operation, input Request) (*http.Request, error) {
	route := mcpRoute(op)
	if route.ID == "" || route.ID == Stream || route.ID == OpenAPI {
		return nil, fmt.Errorf("MCP 工具没有对应的 HTTP 操作")
	}
	address := *base
	address.Path, address.RawPath, address.RawQuery = route.Path, "", input.Query.Encode()
	escaped := route.Path
	for _, part := range strings.Split(route.Path, "/") {
		if !strings.HasPrefix(part, "{") {
			continue
		}
		name := strings.Trim(part, "{}")
		value := input.Params[name]
		if !validMCPPathValue(value) {
			return nil, fmt.Errorf("%s 必须是有效的非空对象 ID", name)
		}
		address.Path = strings.ReplaceAll(address.Path, part, value)
		escaped = strings.ReplaceAll(escaped, part, url.PathEscape(value))
	}
	address.RawPath = escaped
	request, err := http.NewRequestWithContext(ctx, route.Method, address.String(), bytes.NewReader(input.Body))
	if err != nil {
		return nil, fmt.Errorf("创建监控器请求失败：%w", err)
	}
	request.Header.Set("Accept", "application/json")
	if len(input.Body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

func readMCPResponse(status int, body io.Reader) (MCPResponse, error) {
	data, err := io.ReadAll(io.LimitReader(body, mcpResponseLimit+1))
	if err != nil {
		return MCPResponse{}, fmt.Errorf("读取监控器响应失败")
	}
	if len(data) > mcpResponseLimit {
		return MCPResponse{}, fmt.Errorf("监控器响应超过 16 MiB，请缩小查询范围或减少返回字段")
	}
	if status < 200 || status >= 300 {
		var failure struct {
			Error struct {
				Code    string          `json:"code"`
				Message string          `json:"message"`
				Issues  json.RawMessage `json:"issues,omitempty"`
			} `json:"error"`
		}
		if status < 400 || json.Unmarshal(data, &failure) != nil || failure.Error.Code == "" || failure.Error.Message == "" {
			return MCPResponse{}, fmt.Errorf("监控器返回 HTTP %d，请检查地址和服务日志；MCP 不跟随重定向", status)
		}
		data, _ = json.Marshal(failure)
	} else if !json.Valid(data) {
		return MCPResponse{}, fmt.Errorf("监控器没有返回有效 JSON，请检查 HTTP API 地址")
	}
	return MCPResponse{Status: status, Body: bytes.Clone(data)}, nil
}

type mcpOriginKey struct{}

func (s *Server) registerMCP() error {
	server, err := NewMCPServer(MCPOptions{Version: s.options.Version, Logger: s.options.Logger, Caller: MCPCallerFunc(s.callMCP)})
	if err != nil {
		return err
	}
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: true, MaxRequestBodyBytes: MaxValidationRequestBody, PropagateRequestCancellation: true, Logger: s.options.Logger,
	})
	protected, err := s.options.Auth.Protect(ScopeRead, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		ctx := context.WithValue(request.Context(), mcpOriginKey{}, request)
		stream.ServeHTTP(w, request.WithContext(ctx))
	}))
	if err != nil {
		return err
	}
	s.mux.Handle("/mcp", protected)
	return nil
}

func (s *Server) callMCP(ctx context.Context, op Operation, input Request) (MCPResponse, error) {
	origin, ok := ctx.Value(mcpOriginKey{}).(*http.Request)
	if !ok {
		return MCPResponse{}, fmt.Errorf("MCP 调用缺少已认证的 HTTP 请求")
	}
	base := &url.URL{Scheme: "http", Host: origin.Host}
	request, err := mcpAPIRequest(ctx, base, op, input)
	if err != nil {
		return MCPResponse{}, err
	}
	// Re-enter the protected API so scope, revocation, logging and limits cannot diverge.
	request.Header = origin.Header.Clone()
	request.Header.Set("Content-Type", "application/json")
	request.Host, request.RemoteAddr = origin.Host, origin.RemoteAddr
	writer := &mcpResponseWriter{header: http.Header{}}
	s.ServeHTTP(writer, request)
	if writer.overflow {
		return MCPResponse{}, fmt.Errorf("监控器响应超过 16 MiB，请缩小查询范围或减少返回字段")
	}
	status := writer.status
	if status == 0 {
		status = http.StatusOK
	}
	return readMCPResponse(status, &writer.body)
}

type mcpResponseWriter struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	overflow bool
}

func (w *mcpResponseWriter) Header() http.Header { return w.header }
func (w *mcpResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *mcpResponseWriter) Write(data []byte) (int, error) {
	if w.body.Len()+len(data) > mcpResponseLimit {
		w.overflow = true
		return 0, fmt.Errorf("MCP 响应过大")
	}
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(data)
}
