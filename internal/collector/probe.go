package collector

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/secrets"
)

type ProbeResult struct {
	TargetID   string             `json:"targetId"`
	Probe      int                `json:"probe"`
	TS         int64              `json:"ts"`
	OK         bool               `json:"ok"`
	LatencyMS  int64              `json:"latencyMs"`
	StatusCode int                `json:"statusCode,omitempty"`
	Error      string             `json:"error,omitempty"`
	Type       string             `json:"type,omitempty"`
	Message    string             `json:"message,omitempty"`
	Metrics    map[string]float64 `json:"metrics,omitempty"`
	cause      error
}

func (p ProbeResult) Validate() error {
	if p.TargetID == "" || p.Probe < 0 || len(p.Message) > 4096 || len(p.Metrics) > 256 {
		return fmt.Errorf("探测目标、编号、消息或指标数量无效")
	}
	if p.Type != "" && p.Type != "http" && p.Type != "tcp" && p.Type != "script" {
		return fmt.Errorf("探测结果类型无效")
	}
	if p.Type != "script" && len(p.Metrics) != 0 {
		return fmt.Errorf("仅脚本探测接受自定义指标")
	}
	for name, value := range p.Metrics {
		if !scriptMetricName.MatchString(name) || math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("脚本指标名称或数值无效")
		}
	}
	return nil
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type TCPDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type ProbeExecutor interface {
	Execute(context.Context, string, int, config.Probe) ProbeResult
}

type ProbeSink interface {
	WriteProbe(context.Context, ProbeResult) error
}

type Prober struct {
	http    HTTPDoer
	dialer  TCPDialer
	now     func() time.Time
	scripts ScriptRunner
}

func NewProber(now func() time.Time) *Prober {
	return newProber(nil, nil, now)
}

func NewProberForHome(home, hostname string, now func() time.Time) *Prober {
	prober := NewProber(now)
	prober.scripts = nativeScriptRunner{home: home, hostname: hostname, policy: secrets.NativePathPolicy()}
	return prober
}

func newProber(client HTTPDoer, dialer TCPDialer, now func() time.Time) *Prober {
	if dialer == nil {
		dialer = &net.Dialer{KeepAlive: 30 * time.Second}
	}
	if client == nil {
		client = &http.Client{
			Transport: &http.Transport{
				Proxy: nil, DialContext: dialer.DialContext, ForceAttemptHTTP2: true,
				MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return &Prober{http: client, dialer: dialer, now: newInstrumentation(now, nil).now, scripts: nativeScriptRunner{}}
}

func (p *Prober) Close() {
	if client, ok := p.http.(interface{ CloseIdleConnections() }); ok {
		client.CloseIdleConnections()
	}
}

func (p *Prober) Execute(ctx context.Context, targetID string, index int, probe config.Probe) (result ProbeResult) {
	started := p.now()
	result = ProbeResult{TargetID: targetID, Probe: index, TS: started.UTC().UnixMilli(), Type: probe.Type}
	defer func() { result.LatencyMS = max(0, p.now().Sub(started).Milliseconds()) }()
	timeout, err := probe.Timeout.Parse()
	if err != nil || timeout <= 0 {
		result.Error = "探测 timeout 必须为正数合法时长"
		result.cause = err
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	fail := func(prefix string, err error) {
		// Transport errors can contain credential-bearing URLs; persist only safe diagnostics.
		result.cause = fmt.Errorf("%s：%w", prefix, err)
		result.Error = prefix + "，请检查地址、网络或证书"
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
			result.Error = prefix + "：超时，请检查服务响应或增大 timeout"
		} else if errors.Is(err, context.Canceled) {
			result.Error = prefix + "：已取消"
		}
	}
	switch probe.Type {
	case "script":
		result = p.script(ctx, result, probe)
	case "http":
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, probe.URL, nil)
		if err != nil {
			fail("HTTP 探测地址无效", err)
			return
		}
		response, err := p.http.Do(request)
		if response != nil && response.Body != nil {
			defer response.Body.Close()
		}
		if err != nil {
			fail("HTTP 探测请求失败", err)
			return
		}
		if response == nil {
			result.Error = "HTTP 探测未收到响应"
			return
		}
		result.StatusCode = response.StatusCode
		if response.Body != nil {
			if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 32<<10)); err != nil {
				fail("读取 HTTP 探测响应失败", err)
				return
			}
		}
		result.OK = statusMatches(probe.ExpectStatus, response.StatusCode)
		if !result.OK {
			result.Error = fmt.Sprintf("HTTP 状态码 %d 不符合期望 %s", response.StatusCode, expectedStatus(probe.ExpectStatus))
		}
	case "tcp":
		connection, err := p.dialer.DialContext(ctx, "tcp", probe.Address)
		if connection != nil {
			defer connection.Close()
		}
		if err != nil {
			fail("TCP 探测连接失败", err)
			return
		}
		if connection == nil {
			result.Error = "TCP 探测未建立连接"
			return
		}
		result.OK = true
	default:
		result.Error = "探测类型只支持 http、tcp 或 script"
	}
	return
}

func expectedStatus(status config.Status) string {
	if status == "" {
		return "2xx"
	}
	return string(status)
}

func statusMatches(status config.Status, code int) bool {
	if code < 100 || code > 599 {
		return false
	}
	expected := expectedStatus(status)
	if len(expected) == 3 && strings.HasSuffix(expected, "xx") {
		group, err := strconv.Atoi(expected[:1])
		return err == nil && group >= 1 && group <= 5 && code/100 == group
	}
	for _, operator := range []string{"<=", ">=", "<", ">"} {
		if strings.HasPrefix(expected, operator) {
			value, err := strconv.Atoi(strings.TrimPrefix(expected, operator))
			if err != nil || value < 100 || value > 599 {
				return false
			}
			switch operator {
			case "<=":
				return code <= value
			case ">=":
				return code >= value
			case "<":
				return code < value
			case ">":
				return code > value
			}
		}
	}
	value, err := strconv.Atoi(expected)
	return err == nil && value >= 100 && value <= 599 && code == value
}
