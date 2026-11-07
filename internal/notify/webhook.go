package notify

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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/feishu"
)

type WebhookHTTP interface {
	Do(*http.Request) (*http.Response, error)
}
type JSONWebhookClient interface {
	Send(context.Context, string, json.RawMessage) (int, error)
	Close() error
}
type JSONWebhookOptions struct {
	HTTPClient WebhookHTTP
	Now        func() time.Time
	Version    string
}
type jsonWebhookClient struct {
	mu       sync.RWMutex
	endpoint string
	key      []byte
	options  JSONWebhookOptions
	close    func()
	closed   bool
}
type WebhookError struct {
	Status  int
	Message string
	retry   bool
	cause   error
}

func (e *WebhookError) Error() string { return e.Message }
func (e *WebhookError) Unwrap() error { return e.cause }

func ValidateWebhookURL(value []byte) error {
	u, err := url.Parse(string(value))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return fmt.Errorf("Webhook 地址无效，请填写完整的 HTTP 或 HTTPS 地址")
	}
	return nil
}

func DecodeWebhookSecret(value []byte) ([]byte, error) {
	if !bytes.HasPrefix(value, []byte("whsec_")) {
		return nil, fmt.Errorf("签名密钥必须是 whsec_ 加标准 Base64，解码后为 24～64 字节")
	}
	key, err := base64.StdEncoding.DecodeString(string(value[6:]))
	if err != nil || len(key) < 24 || len(key) > 64 || base64.StdEncoding.EncodeToString(key) != string(value[6:]) {
		clear(key)
		return nil, fmt.Errorf("签名密钥格式无效，请提供解码后为 24～64 字节的 whsec_ 密钥")
	}
	return key, nil
}

func WebhookSignature(key []byte, id, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = io.WriteString(mac, id+"."+timestamp+".")
	_, _ = mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func WebhookID(alertID, state, eventKey, channel string) string {
	if eventKey != "" {
		state += "\x00" + eventKey
	}
	return feishu.MessageUUID(alertID, state, channel)
}

func NewJSONWebhookClient(endpoint, secret []byte, options JSONWebhookOptions) (JSONWebhookClient, error) {
	if err := ValidateWebhookURL(endpoint); err != nil {
		return nil, err
	}
	var key []byte
	if len(secret) > 0 {
		var err error
		key, err = DecodeWebhookSecret(secret)
		if err != nil {
			return nil, err
		}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Version == "" {
		options.Version = "dev"
	}
	closeClient := func() {}
	if options.HTTPClient == nil {
		transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second}
		options.HTTPClient = &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		closeClient = transport.CloseIdleConnections
	}
	return &jsonWebhookClient{endpoint: string(endpoint), key: key, options: options, close: closeClient}, nil
}

func (c *jsonWebhookClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.key)
	c.key, c.endpoint, c.closed = nil, "", true
	c.close()
	return nil
}

func (c *jsonWebhookClient) Send(parent context.Context, id string, body json.RawMessage) (int, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed || id == "" || strings.ContainsAny(id, ".\r\n") || !json.Valid(body) {
		return 0, &WebhookError{Message: "Webhook 客户端、投递编号或 JSON 请求体无效"}
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, &WebhookError{Message: "创建 Webhook 请求失败，请由管理员检查地址", cause: err}
	}
	timestamp := strconv.FormatInt(c.options.Now().Unix(), 10)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "lg-monitor/"+c.options.Version)
	request.Header.Set("webhook-id", id)
	request.Header.Set("webhook-timestamp", timestamp)
	if len(c.key) > 0 {
		request.Header.Set("webhook-signature", WebhookSignature(c.key, id, timestamp, body))
	}
	response, err := c.options.HTTPClient.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return 0, &WebhookError{Message: "Webhook 网络请求失败或超时，请检查接收方和网络", retry: true, cause: err}
	}
	if response == nil || response.Body == nil {
		return 0, &WebhookError{Message: "Webhook 返回无效响应", retry: true}
	}
	defer response.Body.Close()
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	status := response.StatusCode
	if status >= 200 && status < 300 {
		return status, nil
	}
	message := fmt.Sprintf("Webhook 返回 HTTP %d，请检查接收方", status)
	if status >= 300 && status < 400 {
		message = "对方要求跳转，请把 Webhook 地址改成跳转后的地址"
	}
	return status, &WebhookError{Status: status, Message: message, retry: status == 408 || status == 429 || status >= 500 && status < 600, cause: readErr}
}
