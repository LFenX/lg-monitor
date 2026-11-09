package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/gorilla/websocket"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type CardAction struct {
	EventID, AppID, OperatorID, ChatID, MessageID string
	Value                                         json.RawMessage
}
type CardActionReply struct{ Type, Message string }
type CardActionHandler func(context.Context, CardAction) CardActionReply
type ActionConnectionState func(string)
type ActionStream interface {
	Run(context.Context, CardActionHandler, ActionConnectionState) error
}
type ActionStreamFactory func(config.FeishuApp, []byte) (ActionStream, error)

type ActionOptions struct {
	Logger     *slog.Logger
	HTTPClient larkcore.HttpClient
	Dialer     *websocket.Dialer
}
type actionSocket interface {
	Start(context.Context) error
	CloseAndWait(context.Context) error
}
type nativeActionStream struct {
	app       config.FeishuApp
	secret    []byte
	options   ActionOptions
	started   atomic.Bool
	newSocket func(*dispatcher.EventDispatcher, ActionConnectionState) actionSocket
}
type actionConnectionError struct{ cause error }

func (e *actionConnectionError) Error() string {
	return "飞书动作连接失败，请检查应用凭证、代理和回调订阅"
}
func (e *actionConnectionError) Unwrap() error { return e.cause }

func NewActionStream(app config.FeishuApp, secret []byte, options ActionOptions) (ActionStream, error) {
	if app.AppID == "" || len(secret) == 0 || (app.Domain != "feishu" && app.Domain != "lark") {
		return nil, fmt.Errorf("飞书动作连接需要有效应用、域名类型和受保护的 App Secret")
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &nativeActionStream{app: app, secret: append([]byte(nil), secret...), options: options}, nil
}

func actionDispatcher(appID string, handler CardActionHandler, logger *slog.Logger) *dispatcher.EventDispatcher {
	d := dispatcher.NewEventDispatcher("", "")
	d.Config.Logger = sdkLogger{logger}
	d.Config.LogLevel = larkcore.LogLevelWarn
	return d.OnP2CardActionTrigger(func(parent context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
		invalid := func() (*callback.CardActionTriggerResponse, error) {
			return &callback.CardActionTriggerResponse{Toast: &callback.Toast{Type: "error", Content: "回调格式或应用身份无效，请从原通知群重试"}}, nil
		}
		if event == nil || event.EventV2Base == nil {
			return invalid()
		}
		header := event.EventV2Base.Header
		if header == nil || event.Schema != "2.0" || header.AppID != appID || header.EventType != "card.action.trigger" || header.EventID == "" || len(header.EventID) > 1024 || event.Event == nil {
			return invalid()
		}
		body := event.Event
		if body.Operator == nil || body.Operator.OpenID == "" || body.Context == nil || body.Context.OpenMessageID == "" || body.Context.OpenChatID == "" || body.Action == nil || body.Action.Tag != "button" || len(body.Action.Value) == 0 {
			return invalid()
		}
		value, err := json.Marshal(body.Action.Value)
		if err != nil || len(value) > 16384 {
			return invalid()
		}
		ctx, cancel := context.WithTimeout(parent, 2*time.Second)
		defer cancel()
		reply := handler(ctx, CardAction{EventID: header.EventID, AppID: appID, OperatorID: body.Operator.OpenID, ChatID: body.Context.OpenChatID, MessageID: body.Context.OpenMessageID, Value: value})
		switch reply.Type {
		case "success", "info", "warning", "error":
		default:
			reply.Type = "info"
		}
		if reply.Message == "" || len(reply.Message) > 2048 {
			reply.Message = "动作未完成，请刷新告警状态后重试"
		}
		return &callback.CardActionTriggerResponse{Toast: &callback.Toast{Type: reply.Type, Content: reply.Message}}, nil
	})
}

func (s *nativeActionStream) Run(ctx context.Context, handler CardActionHandler, state ActionConnectionState) error {
	if handler == nil || ctx == nil || !s.started.CompareAndSwap(false, true) {
		return fmt.Errorf("动作连接需要上下文和处理器，且只能启动一次")
	}
	defer clear(s.secret)
	if state == nil {
		state = func(string) {}
	}
	state("connecting")
	d := actionDispatcher(s.app.AppID, handler, s.options.Logger)
	var socket actionSocket
	closeTransport := func() {}
	if s.newSocket != nil {
		socket = s.newSocket(d, state)
	} else {
		proxy := http.ProxyFromEnvironment
		if s.app.Proxy == "none" {
			proxy = nil
		} else if s.app.Proxy != "" && s.app.Proxy != "auto" {
			u, err := url.Parse(s.app.Proxy)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("飞书动作代理地址无效")
			}
			proxy = http.ProxyURL(u)
		}
		httpClient := s.options.HTTPClient
		if httpClient == nil {
			transport := &http.Transport{Proxy: proxy, DialContext: (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 60 * time.Second}
			httpClient = &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			closeTransport = transport.CloseIdleConnections
		}
		dialer := s.options.Dialer
		if dialer == nil {
			dialer = &websocket.Dialer{Proxy: proxy, HandshakeTimeout: 10 * time.Second}
		}
		domain := "https://open.feishu.cn"
		if s.app.Domain == "lark" {
			domain = "https://open.larksuite.com"
		}
		socket = larkws.NewClient(s.app.AppID, string(s.secret), larkws.WithEventHandler(d), larkws.WithLogger(sdkLogger{s.options.Logger}), larkws.WithLogLevel(larkcore.LogLevelWarn), larkws.WithHttpClient(safeHTTP{httpClient}), larkws.WithWebSocketDialer(dialer), larkws.WithDomain(domain), larkws.WithOnReady(func() { state("connected") }), larkws.WithOnReconnected(func() { state("connected") }), larkws.WithOnReconnecting(func() { state("reconnecting") }), larkws.WithOnError(func(error) { state("error") }), larkws.WithOnDisconnected(func() { state("disconnected") }))
	}
	defer closeTransport()
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = socket.CloseAndWait(closeCtx)
		state("stopped")
	}()
	err := socket.Start(ctx)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return &actionConnectionError{cause: err}
	}
	return nil
}
