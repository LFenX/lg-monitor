package feishu

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type Bot struct {
	Name           string `json:"name"`
	OpenID         string `json:"openId"`
	ActivateStatus int    `json:"activateStatus"`
}
type Scope struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Granted bool   `json:"granted"`
}
type Chat struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MemberCount *int   `json:"memberCount" jsonschema:"nullable"`
}
type CardMessage struct {
	AlertID  string
	State    string
	ChatID   string
	Card     json.RawMessage
	EventKey string
}
type SendResult struct {
	MessageID string `json:"messageId"`
}
type NewChat struct {
	Name    string
	UserIDs []string
}

type Client interface {
	TenantToken(context.Context) (string, error)
	BotInfo(context.Context) (Bot, error)
	AppScopes(context.Context) ([]Scope, error)
	ListChats(context.Context) ([]Chat, error)
	ChatInfo(context.Context, string) (Chat, error)
	SendCard(context.Context, CardMessage) (SendResult, error)
	CreateChat(context.Context, NewChat) (Chat, error)
	Close() error
}

type Options struct {
	Now        func() time.Time
	Logger     *slog.Logger
	HTTPClient larkcore.HttpClient
	Cache      larkcore.Cache
	BaseURL    string
}
type apiClient struct {
	config                *larkcore.Config
	im                    *larkim.V1
	cache                 larkcore.Cache
	tokenMu               sync.Mutex
	appID, secret, domain string
	close                 func()
}

type sdkLogger struct{ logger *slog.Logger }

func (l sdkLogger) Debug(context.Context, ...interface{}) {}
func (l sdkLogger) Info(context.Context, ...interface{})  {}
func (l sdkLogger) Warn(ctx context.Context, _ ...interface{}) {
	l.logger.WarnContext(ctx, "Feishu SDK request warning")
}
func (l sdkLogger) Error(ctx context.Context, _ ...interface{}) {
	l.logger.ErrorContext(ctx, "Feishu SDK request error")
}

type transportError struct{ cause error }

func (e *transportError) Error() string { return "HTTP transport failed" }
func (e *transportError) Unwrap() error { return e.cause }

type safeHTTP struct{ client larkcore.HttpClient }

func (c safeHTTP) Do(request *http.Request) (*http.Response, error) {
	response, err := c.client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return response, &transportError{cause: err}
	}
	return response, nil
}

func NewClient(app config.FeishuApp, secret []byte, options Options) (Client, error) {
	if app.AppID == "" || len(secret) == 0 {
		return nil, fmt.Errorf("飞书应用需要 App ID 和密钥库中的 App Secret")
	}
	if app.Domain != "feishu" && app.Domain != "lark" {
		return nil, fmt.Errorf("飞书域名类型必须为 feishu 或 lark")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	base := "https://open.feishu.cn"
	if app.Domain == "lark" {
		base = "https://open.larksuite.com"
	}
	if options.BaseURL != "" {
		base = options.BaseURL
	}
	closeClient := func() {}
	if options.HTTPClient == nil {
		transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second, MaxIdleConns: 20}
		switch app.Proxy {
		case "", "auto":
			transport.Proxy = http.ProxyFromEnvironment
		case "none":
		default:
			proxy, err := url.Parse(app.Proxy)
			if err != nil || proxy.Host == "" || (proxy.Scheme != "http" && proxy.Scheme != "https") {
				return nil, fmt.Errorf("飞书代理地址无效，请使用 auto、none 或 HTTP 代理地址")
			}
			transport.Proxy = http.ProxyURL(proxy)
		}
		options.HTTPClient = &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		closeClient = transport.CloseIdleConnections
	}
	if options.Cache == nil {
		options.Cache = &tokenCache{values: make(map[string]cacheEntry), now: options.Now}
	}
	cfg := &larkcore.Config{BaseUrl: base, AppId: app.AppID, AppSecret: string(secret), AppType: larkcore.AppTypeSelfBuilt, ReqTimeout: 20 * time.Second, LogLevel: larkcore.LogLevelWarn, HttpClient: safeHTTP{options.HTTPClient}, Logger: sdkLogger{options.Logger}, EnableTokenCache: false}
	larkcore.NewLogger(cfg)
	larkcore.NewSerialization(cfg)
	larkcore.NewHttpClient(cfg)
	return &apiClient{config: cfg, im: larkim.New(cfg), cache: options.Cache, appID: app.AppID, secret: string(secret), domain: app.Domain, close: closeClient}, nil
}

func (c *apiClient) Close() error { c.close(); return nil }

func (c *apiClient) TenantToken(parent context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	value, err := c.cache.Get(ctx, "tenant")
	if err != nil {
		return "", c.classify("", 0, larkcore.CodeError{}, err)
	}
	if value != "" {
		return value, nil
	}
	response, err := larkcore.Request(ctx, &larkcore.ApiReq{HttpMethod: http.MethodPost, ApiPath: larkcore.TenantAccessTokenInternalUrlPath, Body: &larkcore.SelfBuiltTenantAccessTokenReq{AppID: c.appID, AppSecret: c.secret}, SupportedAccessTokenTypes: []larkcore.AccessTokenType{larkcore.AccessTokenTypeNone}}, c.config)
	if err != nil {
		return "", c.classify("", 0, larkcore.CodeError{}, err)
	}
	var token larkcore.TenantAccessTokenResp
	if err := json.Unmarshal(response.RawBody, &token); err != nil {
		return "", c.classify("", response.StatusCode, larkcore.CodeError{}, err)
	}
	if token.Code != 0 || response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", c.classify(larkcore.TenantAccessTokenInternalUrlPath, response.StatusCode, token.CodeError, nil)
	}
	if token.TenantAccessToken == "" || token.Expire <= 0 {
		return "", &Error{Kind: API, Message: "飞书返回的令牌或有效期无效", retryable: true}
	}
	ttl := time.Duration(token.Expire) * time.Second
	if err := c.cache.Set(ctx, "tenant", token.TenantAccessToken, ttl-min(3*time.Minute, ttl/10)); err != nil {
		return "", c.classify("", 0, larkcore.CodeError{}, err)
	}
	return token.TenantAccessToken, nil
}

func (c *apiClient) call(parent context.Context, path string, request func(context.Context, string) (*larkcore.ApiResp, error)) (*larkcore.ApiResp, error) {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.TenantToken(ctx)
		if err != nil {
			return nil, err
		}
		response, err := request(ctx, token)
		if err != nil {
			return nil, c.classify(path, 0, larkcore.CodeError{}, err)
		}
		var code larkcore.CodeError
		if err := json.Unmarshal(response.RawBody, &code); err != nil {
			return nil, c.classify(path, response.StatusCode, code, err)
		}
		if code.Code == 0 && response.StatusCode >= 200 && response.StatusCode < 300 {
			return response, nil
		}
		if attempt == 0 && (code.Code == 99991663 || code.Code == 99991665) {
			c.tokenMu.Lock()
			current, _ := c.cache.Get(ctx, "tenant")
			if current == token {
				_ = c.cache.Set(ctx, "tenant", "", 0)
			}
			c.tokenMu.Unlock()
			continue
		}
		return nil, c.classify(path, response.StatusCode, code, nil)
	}
	return nil, &Error{Kind: Credentials, Message: "飞书凭证刷新失败，请重新检查 App ID 和 App Secret"}
}

func (c *apiClient) get(ctx context.Context, path string) (*larkcore.ApiResp, error) {
	return c.call(ctx, path, func(ctx context.Context, token string) (*larkcore.ApiResp, error) {
		return larkcore.Request(ctx, &larkcore.ApiReq{HttpMethod: http.MethodGet, ApiPath: path, SupportedAccessTokenTypes: []larkcore.AccessTokenType{larkcore.AccessTokenTypeTenant}}, c.config, larkcore.WithTenantAccessToken(token))
	})
}

func (c *apiClient) BotInfo(ctx context.Context) (Bot, error) {
	response, err := c.get(ctx, "/open-apis/bot/v3/info")
	if err != nil {
		return Bot{}, err
	}
	type botData struct {
		Name   string `json:"app_name"`
		OpenID string `json:"open_id"`
		Status int    `json:"activate_status"`
	}
	var body struct {
		Bot  *botData        `json:"bot"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response.RawBody, &body); err != nil {
		return Bot{}, c.classify("", 0, larkcore.CodeError{}, err)
	}
	bot := body.Bot
	if bot == nil && len(body.Data) > 0 {
		var nested struct {
			Bot *botData `json:"bot"`
		}
		_ = json.Unmarshal(body.Data, &nested)
		bot = nested.Bot
		if bot == nil {
			bot = &botData{}
			_ = json.Unmarshal(body.Data, bot)
		}
	}
	if bot == nil || bot.Status != 2 {
		return Bot{}, &Error{Kind: BotDisabled, Message: "机器人未启用，请去开发者后台“应用能力”中添加“机器人”"}
	}
	return Bot{Name: bot.Name, OpenID: bot.OpenID, ActivateStatus: bot.Status}, nil
}

func (c *apiClient) AppScopes(ctx context.Context) ([]Scope, error) {
	response, err := c.get(ctx, "/open-apis/application/v6/scopes")
	if err != nil {
		return nil, err
	}
	var body struct {
		Data struct {
			Scopes []struct {
				Name  string `json:"scope_name"`
				Type  string `json:"scope_type"`
				Grant int    `json:"grant_status"`
			} `json:"scopes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.RawBody, &body); err != nil {
		return nil, c.classify("", 0, larkcore.CodeError{}, err)
	}
	result := make([]Scope, 0, len(body.Data.Scopes))
	for _, scope := range body.Data.Scopes {
		result = append(result, Scope{Name: scope.Name, Type: scope.Type, Granted: scope.Grant == 1})
	}
	return result, nil
}

func (c *apiClient) ListChats(ctx context.Context) ([]Chat, error) {
	result := make([]Chat, 0)
	page := ""
	seenPages := make(map[string]bool)
	seenChats := make(map[string]bool)
	for {
		var response *larkim.ListChatResp
		_, err := c.call(ctx, "/open-apis/im/v1/chats", func(ctx context.Context, token string) (*larkcore.ApiResp, error) {
			var err error
			response, err = c.im.Chat.List(ctx, larkim.NewListChatReqBuilder().PageSize(100).PageToken(page).Build(), larkcore.WithTenantAccessToken(token))
			if err != nil {
				return nil, err
			}
			return response.ApiResp, nil
		})
		if err != nil {
			return nil, err
		}
		if response.Data == nil {
			return nil, &Error{Kind: API, Message: "飞书群列表缺少数据"}
		}
		for _, item := range response.Data.Items {
			if item != nil && item.ChatId != nil && !seenChats[*item.ChatId] {
				seenChats[*item.ChatId] = true
				result = append(result, Chat{ID: *item.ChatId, Name: stringValue(item.Name)})
			}
		}
		if response.Data.HasMore == nil || !*response.Data.HasMore {
			return result, nil
		}
		page = stringValue(response.Data.PageToken)
		if page == "" || seenPages[page] {
			return nil, &Error{Kind: API, Message: "飞书群列表分页标记无效或重复"}
		}
		seenPages[page] = true
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (c *apiClient) ChatInfo(ctx context.Context, id string) (Chat, error) {
	if id == "" {
		return Chat{}, fmt.Errorf("查询群详情需要群 ID")
	}
	var response *larkim.GetChatResp
	_, err := c.call(ctx, "/open-apis/im/v1/chats/"+id, func(ctx context.Context, token string) (*larkcore.ApiResp, error) {
		var err error
		response, err = c.im.Chat.Get(ctx, larkim.NewGetChatReqBuilder().ChatId(id).UserIdType("open_id").Build(), larkcore.WithTenantAccessToken(token))
		if err != nil {
			return nil, err
		}
		return response.ApiResp, nil
	})
	if err != nil {
		return Chat{}, err
	}
	if response.Data == nil {
		return Chat{}, &Error{Kind: API, Message: "飞书群详情缺少数据"}
	}
	chat := Chat{ID: id, Name: stringValue(response.Data.Name)}
	if response.Data.UserCount != nil {
		count, err := strconv.Atoi(*response.Data.UserCount)
		if err != nil || count < 0 {
			return Chat{}, &Error{Kind: API, Message: "飞书返回的群人数格式无效"}
		}
		chat.MemberCount = &count
	}
	return chat, nil
}
func MessageUUID(alertID, state, chatID string) string {
	sum := sha256.Sum256([]byte(alertID + "\x00" + state + "\x00" + chatID))
	return hex.EncodeToString(sum[:20])
}

func (c *apiClient) SendCard(ctx context.Context, message CardMessage) (SendResult, error) {
	if message.AlertID == "" || message.State == "" || message.ChatID == "" || !json.Valid(message.Card) {
		return SendResult{}, fmt.Errorf("发送卡片需要告警 ID、状态、群和合法 JSON")
	}
	var response *larkim.CreateMessageResp
	_, err := c.call(ctx, "/open-apis/im/v1/messages", func(ctx context.Context, token string) (*larkcore.ApiResp, error) {
		identity := message.State
		if message.EventKey != "" {
			identity += "\x00" + message.EventKey
		}
		body := larkim.NewCreateMessageReqBodyBuilder().ReceiveId(message.ChatID).MsgType("interactive").Content(string(message.Card)).Uuid(MessageUUID(message.AlertID, identity, message.ChatID)).Build()
		var err error
		response, err = c.im.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().ReceiveIdType("chat_id").Body(body).Build(), larkcore.WithTenantAccessToken(token))
		if err != nil {
			return nil, err
		}
		return response.ApiResp, nil
	})
	if err != nil {
		return SendResult{}, err
	}
	if response.Data == nil || response.Data.MessageId == nil || *response.Data.MessageId == "" {
		return SendResult{}, &Error{Kind: API, Message: "飞书发送响应缺少消息 ID", retryable: true}
	}
	return SendResult{MessageID: *response.Data.MessageId}, nil
}

func (c *apiClient) CreateChat(ctx context.Context, chat NewChat) (Chat, error) {
	if strings.TrimSpace(chat.Name) == "" {
		return Chat{}, fmt.Errorf("新建告警群需要群名称")
	}
	var response *larkim.CreateChatResp
	_, err := c.call(ctx, "/open-apis/im/v1/chats", func(ctx context.Context, token string) (*larkcore.ApiResp, error) {
		body := larkim.NewCreateChatReqBodyBuilder().Name(chat.Name).UserIdList(chat.UserIDs).Build()
		var err error
		response, err = c.im.Chat.Create(ctx, larkim.NewCreateChatReqBuilder().UserIdType("open_id").Body(body).Build(), larkcore.WithTenantAccessToken(token))
		if err != nil {
			return nil, err
		}
		return response.ApiResp, nil
	})
	if err != nil {
		return Chat{}, err
	}
	if response.Data == nil || response.Data.ChatId == nil {
		return Chat{}, &Error{Kind: API, Message: "飞书建群响应缺少群 ID"}
	}
	return Chat{ID: *response.Data.ChatId, Name: stringValue(response.Data.Name)}, nil
}
