package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/rules"
)

type TrayHTTPStatus struct {
	Firing   int
	Critical int
	Scope    api.Scope
	PanelURL string
}

type TrayHTTPReader struct {
	base      *url.URL
	transport http.RoundTripper
}

func NewTrayHTTPReader(address string, transport http.RoundTripper) (*TrayHTTPReader, error) {
	base, err := url.Parse(address)
	if err != nil || !trayPanelURL(address) || (base.Path != "" && base.Path != "/") || base.Opaque != "" {
		return nil, fmt.Errorf("监控器地址必须是完整 HTTP/HTTPS 地址，不能包含路径、凭据、查询参数或片段")
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &TrayHTTPReader{base: base, transport: transport}, nil
}

func (r *TrayHTTPReader) Read(ctx context.Context) (TrayHTTPStatus, error) {
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	address := *r.base
	address.Path, address.RawPath, address.RawQuery = "/api/v1/alerts", "", "state=firing"
	request, err := http.NewRequestWithContext(call, http.MethodGet, address.String(), nil)
	if err != nil {
		return TrayHTTPStatus{}, fmt.Errorf("创建托盘状态请求失败：%w", err)
	}
	request.Header.Set("Accept", "application/json")
	// The caller supplies authentication; one RoundTrip cannot follow a credential-bearing redirect.
	response, err := r.transport.RoundTrip(request)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		if call.Err() != nil {
			return TrayHTTPStatus{}, fmt.Errorf("读取托盘状态已取消或超时：%w", call.Err())
		}
		return TrayHTTPStatus{}, fmt.Errorf("连接监控器失败，请检查地址、网络和认证配置")
	}
	if response == nil || response.Body == nil {
		return TrayHTTPStatus{}, fmt.Errorf("监控器未返回完整的 HTTP 响应")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return TrayHTTPStatus{}, fmt.Errorf("监控器返回 HTTP %d，请检查连接和令牌权限；托盘不跟随重定向", response.StatusCode)
	}
	scope := api.Scope(response.Header.Get("X-Lgmon-Scope"))
	if !scope.Valid() {
		return TrayHTTPStatus{}, fmt.Errorf("监控器未返回有效的访问权限，请检查 API 地址和版本")
	}
	const limit = 16 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return TrayHTTPStatus{}, fmt.Errorf("读取监控器告警列表失败")
	}
	if len(body) > limit {
		return TrayHTTPStatus{}, fmt.Errorf("监控器告警列表超过 16 MiB，无法显示托盘状态")
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '[' {
		return TrayHTTPStatus{}, fmt.Errorf("监控器告警列表格式无效，无法显示托盘状态")
	}
	var alerts []struct {
		State    string `json:"state"`
		Severity string `json:"severity"`
	}
	if err := json.Unmarshal(body, &alerts); err != nil {
		return TrayHTTPStatus{}, fmt.Errorf("监控器告警列表格式无效，无法显示托盘状态")
	}
	critical := 0
	for _, alert := range alerts {
		if alert.Severity == "critical" {
			critical++
		}
		if alert.State != rules.StateFiring {
			return TrayHTTPStatus{}, fmt.Errorf("监控器返回的告警状态与查询不符，无法显示托盘状态")
		}
	}
	return TrayHTTPStatus{Firing: len(alerts), Critical: critical, Scope: scope, PanelURL: r.base.String()}, nil
}
