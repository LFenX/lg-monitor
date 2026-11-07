package feishu

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
)

const WebhookBodyLimit = 20 * 1024

type WebhookClient interface {
	SendCard(context.Context, CardMessage) (SendResult, error)
	Close() error
}

type WebhookOptions struct {
	HTTPClient larkcore.HttpClient
	Now        func() time.Time
}

type webhookClient struct {
	mu       sync.RWMutex
	endpoint string
	secret   []byte
	client   larkcore.HttpClient
	now      func() time.Time
	close    func()
	closed   bool
}

var hookToken = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func ValidateWebhookURL(value []byte) error {
	u, err := url.Parse(string(value))
	const prefix = "/open-apis/bot/v2/hook/"
	if err != nil || u.Scheme != "https" || (u.Host != "open.feishu.cn" && u.Host != "open.larksuite.com") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || !strings.HasPrefix(u.Path, prefix) || !hookToken.MatchString(strings.TrimPrefix(u.Path, prefix)) {
		return fmt.Errorf("飞书 Webhook 地址无效，请从群内自定义机器人复制完整的 HTTPS 地址")
	}
	return nil
}

func NewWebhookClient(endpoint, signingSecret []byte, options WebhookOptions) (WebhookClient, error) {
	if err := ValidateWebhookURL(endpoint); err != nil {
		return nil, err
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	closeClient := func() {}
	if options.HTTPClient == nil {
		transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 20}
		options.HTTPClient = &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		closeClient = transport.CloseIdleConnections
	}
	return &webhookClient{endpoint: string(endpoint), secret: bytes.Clone(signingSecret), client: options.HTTPClient, now: options.Now, close: closeClient}, nil
}

func (c *webhookClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.secret)
	c.secret, c.endpoint, c.closed = nil, "", true
	c.close()
	return nil
}

func (c *webhookClient) SendCard(parent context.Context, message CardMessage) (SendResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return SendResult{}, &Error{Kind: API, Message: "飞书 Webhook 客户端已关闭"}
	}
	var card map[string]json.RawMessage
	if err := json.Unmarshal(message.Card, &card); err != nil || card == nil {
		return SendResult{}, &Error{Kind: CardRejected, Message: "飞书 Webhook 卡片必须是有效的 JSON 对象，请检查模板"}
	}
	body := struct {
		Type      string          `json:"msg_type"`
		Card      json.RawMessage `json:"card"`
		Timestamp string          `json:"timestamp,omitempty"`
		Sign      string          `json:"sign,omitempty"`
	}{Type: "interactive", Card: message.Card}
	if len(c.secret) > 0 {
		body.Timestamp = strconv.FormatInt(c.now().Unix(), 10)
		key := append([]byte(body.Timestamp+"\n"), c.secret...)
		mac := hmac.New(sha256.New, key)
		clear(key)
		body.Sign = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	data, err := json.Marshal(body)
	if err != nil || len(data) > WebhookBodyLimit {
		return SendResult{}, &Error{Kind: CardRejected, Message: "飞书 Webhook 请求超过 20 KB 或卡片无法编码，请减少模板内容"}
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return SendResult{}, &Error{Kind: API, Message: "创建飞书 Webhook 请求失败，请重新保存地址", cause: err}
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return SendResult{}, &Error{Kind: Network, Message: "飞书 Webhook 网络请求失败，请检查网络和代理设置", cause: err}
	}
	if response == nil || response.Body == nil {
		return SendResult{}, &Error{Kind: API, Message: "飞书 Webhook 返回空响应，请检查机器人地址"}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return SendResult{}, webhookFailure(response.StatusCode, 0)
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil {
		return SendResult{}, &Error{Kind: Network, Message: "读取飞书 Webhook 响应失败，请检查网络", cause: err}
	}
	var result struct {
		Code *int `json:"code"`
	}
	if len(data) > 64*1024 || json.Unmarshal(data, &result) != nil || result.Code == nil {
		return SendResult{}, &Error{Kind: API, Message: "飞书 Webhook 返回格式无效，请检查机器人地址"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || *result.Code != 0 {
		return SendResult{}, webhookFailure(response.StatusCode, *result.Code)
	}
	// Custom bots do not return a message ID or offer application-message deduplication.
	return SendResult{}, nil
}

func webhookFailure(status, code int) error {
	switch {
	case status == http.StatusTooManyRequests || code == 11232 || code == 11233:
		return &Error{Kind: RateLimited, Code: code, Message: "飞书 Webhook 请求过于频繁，请稍后重试"}
	case status >= 500:
		return &Error{Kind: Network, Code: code, Message: "飞书 Webhook 服务暂时不可用，请稍后重试"}
	case code == 19021:
		return &Error{Kind: Credentials, Code: code, Message: "飞书 Webhook 签名校验失败，请检查签名密钥和系统时间"}
	case code == 19022 || code == 19024:
		return &Error{Kind: API, Code: code, Message: "飞书 Webhook 安全校验失败，请检查机器人关键词和 IP 白名单"}
	case code == 9499 || code == 230099:
		return &Error{Kind: CardRejected, Code: code, Message: "飞书 Webhook 拒绝了卡片，请检查模板内容"}
	default:
		return &Error{Kind: API, Code: code, Message: fmt.Sprintf("飞书 Webhook 请求失败（HTTP %d，错误码 %d），请检查机器人配置", status, code)}
	}
}
