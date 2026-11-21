// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/web"
)

type HTTPNetwork interface {
	Listen(context.Context, string) (net.Listener, error)
	Addresses(context.Context) ([]string, error)
}

type NativeHTTPNetwork struct{}

func (NativeHTTPNetwork) Listen(ctx context.Context, address string) (net.Listener, error) {
	return (&net.ListenConfig{}).Listen(ctx, "tcp", address)
}
func (NativeHTTPNetwork) Addresses(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("读取 HTTP 本机地址失败：%w", err)
	}
	result := []string{}
	for _, item := range items {
		switch address := item.(type) {
		case *net.IPNet:
			result = append(result, address.IP.String())
		case *net.IPAddr:
			result = append(result, address.IP.String())
		}
	}
	return result, nil
}

type HTTPNodeOptions struct {
	Version string
	Config  *config.Manager
	Network HTTPNetwork
	Tokens  api.TokenLookup
	Entropy io.Reader
	Actions map[api.Operation]api.Action
	Broker  *api.Broker
	Now     func() time.Time
	Logger  *slog.Logger
	Ready   func(string)
	// Assistant is nil on nodes that cannot reach monitoring data.
	Assistant api.AssistantFunc
	// Personal is true when the monitor runs as the signed-in user with its own home.
	Personal bool
	// JobKeys lets batch jobs report with their own keys.
	JobKeys api.JobKeys
	// LangFile persists the UI display language preference across windows and restarts.
	LangFile string
}

// localScope is what the page opened on this computer may do. The startup window and local
// browser default to config unless settings.localAccess explicitly restricts them to read.
func localScope(settings config.Settings, _ bool) api.Scope {
	if settings.LocalAccess == "read" {
		return api.ScopeRead
	}
	return api.ScopeConfig
}

type HTTPNode struct {
	options HTTPNodeOptions
	auth    *api.Authenticator
	handler *api.Server
	cancel  context.CancelFunc
	done    chan struct{}
	err     error
}

type httpInstance struct {
	server   *http.Server
	listener net.Listener
	cancel   context.CancelFunc
	done     chan struct{}
	err      error
}

type httpLog struct{ logger *slog.Logger }

func (w httpLog) Write(p []byte) (int, error) {
	w.logger.Warn("HTTP connection failure")
	return len(p), nil
}

func NewHTTPNode(parent context.Context, opts HTTPNodeOptions) (*HTTPNode, error) {
	if opts.Config == nil || opts.Tokens == nil || opts.Broker == nil || opts.Now == nil {
		return nil, fmt.Errorf("HTTP 监听需要配置、令牌、实时事件和时钟依赖")
	}
	if opts.Network == nil {
		opts.Network = NativeHTTPNetwork{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	settings := opts.Config.Snapshot().Settings
	listener, policy, err := httpListen(parent, opts, settings)
	if err != nil {
		return nil, err
	}
	auth, err := api.NewAuthenticator(api.AuthOptions{Tokens: opts.Tokens, Entropy: opts.Entropy, Hosts: policy, Logger: opts.Logger, Now: opts.Now})
	if err != nil {
		listener.Close()
		return nil, err
	}
	_ = auth.SetLocalScope(localScope(settings, opts.Personal))
	auth.SetLANWeb(settings.LAN.Web)
	handler, err := api.NewServer(api.ServerOptions{Version: opts.Version, Auth: auth, Actions: opts.Actions, Broker: opts.Broker, Now: opts.Now, Logger: opts.Logger, Frontend: web.NewHandlerWithWebSession(auth.WebSession), Assistant: opts.Assistant, JobKeys: opts.JobKeys, LangFile: opts.LangFile})
	if err != nil {
		listener.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	watchDashboardEvents(ctx, opts.Config, opts.Broker)
	node := &HTTPNode{options: opts, auth: auth, handler: handler, cancel: cancel, done: make(chan struct{})}
	instance := node.serve(ctx, listener, settings)
	updates := opts.Config.Subscribe(ctx)
	go node.run(ctx, instance, settings, updates)
	return node, nil
}

func httpListen(ctx context.Context, opts HTTPNodeOptions, settings config.Settings) (net.Listener, *api.HostPolicy, error) {
	listener, err := opts.Network.Listen(ctx, settings.HTTPAddress())
	if err != nil {
		return nil, nil, fmt.Errorf("监听 HTTP API 地址 %s 失败：%w", settings.HTTPAddress(), err)
	}
	policy, err := httpHosts(ctx, opts, settings)
	if err != nil {
		listener.Close()
		return nil, nil, err
	}
	return listener, policy, nil
}

func httpHosts(ctx context.Context, opts HTTPNodeOptions, settings config.Settings) (*api.HostPolicy, error) {
	addresses, err := opts.Network.Addresses(ctx)
	if err != nil {
		return nil, err
	}
	return api.NewHostPolicy(settings.HTTPAddress(), settings.PublicBaseURL, addresses)
}

func (n *HTTPNode) serve(ctx context.Context, listener net.Listener, settings config.Settings) *httpInstance {
	base, cancel := context.WithCancel(ctx)
	instance := &httpInstance{listener: listener, cancel: cancel, done: make(chan struct{})}
	instance.server = &http.Server{Handler: n.handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024, ErrorLog: log.New(httpLog{n.options.Logger}, "", 0), BaseContext: func(net.Listener) context.Context { return base }}
	go func() { instance.err = instance.server.Serve(listener); close(instance.done) }()
	n.options.Logger.Info("HTTP API listener started", "listen", listener.Addr().String())
	if settings.HTTPWarning() != "" {
		n.options.Logger.Warn("HTTP panel is exposed without encryption; network peers may intercept tokens; listen on a Tailscale address", "listen", settings.HTTPAddress())
	}
	if n.options.Ready != nil {
		n.options.Ready(listener.Addr().String())
	}
	return instance
}

func closeHTTP(instance *httpInstance) {
	instance.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := instance.server.Shutdown(ctx); err != nil {
		_ = instance.server.Close()
	}
	<-instance.done
}

func (n *HTTPNode) run(ctx context.Context, current *httpInstance, settings config.Settings, updates <-chan *config.Config) {
	defer close(n.done)
	defer n.cancel()
	defer func() { closeHTTP(current) }()
	for {
		select {
		case <-ctx.Done():
			return
		case <-current.done:
			if ctx.Err() == nil {
				if current.err == nil {
					n.err = fmt.Errorf("HTTP API 监听提前结束")
				} else {
					n.err = fmt.Errorf("HTTP API 监听提前结束：%w", current.err)
				}
			}
			return
		case cfg, ok := <-updates:
			if !ok || cfg == nil {
				if ctx.Err() == nil {
					n.err = fmt.Errorf("HTTP 配置订阅提前关闭")
				}
				return
			}
			next := cfg.Settings
			_ = n.auth.SetLocalScope(localScope(next, n.options.Personal))
			n.auth.SetLANWeb(next.LAN.Web)
			if next.HTTPAddress() == settings.HTTPAddress() {
				if next.PublicBaseURL != settings.PublicBaseURL || next.LAN.Web != settings.LAN.Web {
					policy, err := httpHosts(ctx, n.options, next)
					if err != nil {
						n.options.Logger.Warn("HTTP host policy update failed; retaining previous policy")
						continue
					}
					_ = n.auth.SetHosts(policy)
					settings = next
				}
				continue
			}
			// Switching 127.0.0.1:7444 <-> 0.0.0.0:7444 shares the same TCP port; on Windows, binding
			// the wildcard address while loopback is still bound fails with WSAEADDRINUSE, so close first.
			_, oldPort, _ := net.SplitHostPort(settings.HTTPAddress())
			_, newPort, _ := net.SplitHostPort(next.HTTPAddress())
			if oldPort != "" && oldPort == newPort {
				closeHTTP(current)
				listener, policy, err := httpListen(ctx, n.options, next)
				if err != nil {
					n.options.Logger.Warn("HTTP listener update failed; restoring previous listener", "error", err)
					if prevListener, prevPolicy, prevErr := httpListen(ctx, n.options, settings); prevErr == nil {
						_ = n.auth.SetHosts(prevPolicy)
						current = n.serve(ctx, prevListener, settings)
					} else {
						n.err = fmt.Errorf("恢复 HTTP 监听失败：%w", prevErr)
						return
					}
					continue
				}
				_ = n.auth.SetHosts(policy)
				current = n.serve(ctx, listener, next)
				settings = next
				continue
			}
			listener, policy, err := httpListen(ctx, n.options, next)
			if err != nil {
				n.options.Logger.Warn("HTTP listener update failed; retaining previous listener", "error", err)
				continue
			}
			_ = n.auth.SetHosts(policy)
			old := current
			current = n.serve(ctx, listener, next)
			settings = next
			closeHTTP(old)
		}
	}
}

func (n *HTTPNode) Close()      { n.cancel() }
func (n *HTTPNode) Wait() error { <-n.done; return n.err }

func (n *HTTPNode) LocalSession(r *http.Request) (string, bool, error) { return n.auth.LocalSession(r) }

func listenerURL(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return ""
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}
