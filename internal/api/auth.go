// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LFenX/lg-monitor/internal/secrets"
)

type Principal struct {
	Name    string `json:"name"`
	Scope   Scope  `json:"scope"`
	Local   bool   `json:"local,omitempty"`
	Guest   bool   `json:"guest,omitempty"`
	LANFull bool   `json:"-"`
}
type TokenLookup interface {
	FindToken(context.Context, [32]byte) (Principal, bool, error)
}
type TokenLookupFunc func(context.Context, [32]byte) (Principal, bool, error)

func (f TokenLookupFunc) FindToken(ctx context.Context, hash [32]byte) (Principal, bool, error) {
	return f(ctx, hash)
}

type principalKey struct{}

func CurrentPrincipal(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(Principal)
	return principal, ok
}

// WithPrincipal attaches a Principal to ctx (used by tests and internal callers).
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

type AuthOptions struct {
	LANKeys      LANKeyStore
	Tokens       TokenLookup
	Entropy      io.Reader
	Hosts        *HostPolicy
	Logger       *slog.Logger
	Now          func() time.Time
	KeyProtector func() (secrets.Protector, error)
}
type Authenticator struct {
	keyProtector  func() (secrets.Protector, error)
	lanKeys       LANKeyStore
	sessionSecret string
	now           func() time.Time
	tokens        TokenLookup
	local         Token
	guest         Token
	localScope    atomic.Value
	lanWeb        atomic.Bool
	hosts         atomic.Pointer[HostPolicy]
	visitors      *VisitorTracker
	logger        *slog.Logger
}

func NewAuthenticator(opts AuthOptions) (*Authenticator, error) {
	if opts.Tokens == nil || opts.Hosts == nil {
		return nil, fmt.Errorf("HTTP API 认证需要令牌查询接口和主机白名单")
	}
	local, err := NewToken(ScopeRead, "local-browser", opts.Entropy)
	if err != nil {
		return nil, err
	}
	guest, err := NewToken(ScopeRead, "lan-visitor", rand.Reader)
	if err != nil {
		return nil, err
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	protector := opts.KeyProtector
	if protector == nil {
		protector = secrets.MachineProtector
	}
	auth := &Authenticator{keyProtector: protector, lanKeys: opts.LANKeys, sessionSecret: rand.Text(), now: opts.Now, tokens: opts.Tokens, local: local, guest: guest, visitors: newVisitorTracker(opts.Now), logger: opts.Logger}
	auth.localScope.Store(ScopeRead)
	auth.hosts.Store(opts.Hosts)
	return auth, nil
}

// SetLANWeb toggles browser access from private LAN / Tailscale peers.
func (a *Authenticator) SetLANWeb(enabled bool) { a.lanWeb.Store(enabled) }

func (a *Authenticator) LANWeb() bool { return a.lanWeb.Load() }

// Visitors lists active LAN peers currently viewing the dashboard.
func (a *Authenticator) Visitors() []LANVisitor { return a.visitors.List() }

func (a *Authenticator) EnterGuestStream(ip string) func() { return a.visitors.EnterStream(ip) }

// SetLocalScope decides what the page opened on this computer may do. It never grants admin:
// settings, secrets and team membership always need a token someone created on purpose.
func (a *Authenticator) SetLocalScope(scope Scope) error {
	if scope != ScopeRead && scope != ScopeConfig {
		return fmt.Errorf("本机网页权限只能是 read 或 config")
	}
	a.localScope.Store(scope)
	return nil
}

func (a *Authenticator) LocalScope() Scope { return a.localScope.Load().(Scope) }

func (a *Authenticator) SetHosts(policy *HostPolicy) error {
	if policy == nil {
		return fmt.Errorf("HTTP 主机白名单不能为空")
	}
	a.hosts.Store(policy)
	return nil
}

func (a *Authenticator) CheckHost(r *http.Request) error {
	if a.LANWeb() {
		return a.hosts.Load().CheckAllowingPrivateLAN(r)
	}
	return a.hosts.Load().Check(r)
}

var apiGzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return w
	},
}

func acceptsGzip(r *http.Request) bool {
	if r == nil {
		return false
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		token, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil || token != "gzip" {
			continue
		}
		if q, exists := params["q"]; exists {
			quality, err := strconv.ParseFloat(q, 64)
			return err == nil && quality > 0 && quality <= 1
		}
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	writeJSONForRequest(w, nil, status, value)
}

func writeJSONForRequest(w http.ResponseWriter, r *http.Request, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if r != nil && r.Method == http.MethodGet {
		w.Header().Add("Vary", "Accept-Encoding")
	}
	if r != nil && r.Method == http.MethodGet && acceptsGzip(r) {
		var raw bytes.Buffer
		if err := json.NewEncoder(&raw).Encode(value); err == nil {
			if raw.Len() >= 1024 {
				var compressed bytes.Buffer
				gz := apiGzipPool.Get().(*gzip.Writer)
				gz.Reset(&compressed)
				_, werr := gz.Write(raw.Bytes())
				cerr := gz.Close()
				gz.Reset(io.Discard)
				apiGzipPool.Put(gz)
				if werr == nil && cerr == nil && compressed.Len() < raw.Len() {
					w.Header().Set("Content-Encoding", "gzip")
					w.Header().Set("Content-Length", strconv.Itoa(compressed.Len()))
					w.WriteHeader(status)
					_, _ = w.Write(compressed.Bytes())
					return
				}
			}
			w.Header().Set("Content-Length", strconv.Itoa(raw.Len()))
			w.WriteHeader(status)
			_, _ = w.Write(raw.Bytes())
			return
		}
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func (a *Authenticator) unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="lgmon"`)
	writeError(w, 401, "unauthorized", "请提供有效的访问令牌")
}

func (a *Authenticator) LocalSession(r *http.Request) (string, bool, error) {
	if err := a.CheckHost(r); err != nil {
		return "", false, err
	}
	if !localRequest(r) {
		return "", false, nil
	}
	return a.local.Value, true, nil
}

// WebSession returns the browser bootstrap session: local owner on loopback, or read-only guest on LAN when enabled.
func (a *Authenticator) WebSession(r *http.Request) (token string, local bool, guest bool, err error) {
	if err := a.CheckHost(r); err != nil {
		return "", false, false, err
	}
	if localRequest(r) {
		return a.local.Value, true, false, nil
	}
	if a.LANWeb() {
		if ip, ok := lanVisitorRequest(r); ok {
			a.visitors.Touch(ip)
			return a.guest.Value, false, true, nil
		}
	}
	return "", false, false, nil
}

func (a *Authenticator) authenticate(r *http.Request) (Principal, bool, error) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return Principal{}, false, nil
	}
	fields := strings.Fields(values[0])
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || len(fields[1]) > 512 {
		return Principal{}, false, nil
	}
	if strings.HasPrefix(fields[1], lanSessionPrefix) {
		return a.authenticateLANSession(r, fields[1])
	}
	hash := TokenHash(fields[1])
	if subtle.ConstantTimeCompare(hash[:], a.local.Digest[:]) == 1 {
		if !localRequest(r) {
			return Principal{}, false, nil
		}
		return Principal{Name: "local-browser", Scope: a.LocalScope(), Local: true}, true, nil
	}
	if subtle.ConstantTimeCompare(hash[:], a.guest.Digest[:]) == 1 {
		if !a.LANWeb() {
			return Principal{}, false, nil
		}
		if _, ok := lanVisitorRequest(r); !ok {
			return Principal{}, false, nil
		}
		// The bootstrap bearer always remains read-only, even after another tab unlocks.
		return Principal{Name: "lan-visitor", Scope: ScopeRead, Local: false, Guest: true}, true, nil
	}
	principal, found, err := a.tokens.FindToken(r.Context(), hash)
	return principal, found && principal.Scope.Valid(), err
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *statusWriter) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(value)
}

func (a *Authenticator) Protect(minimum Scope, next http.Handler) (http.Handler, error) {
	if !minimum.Valid() || next == nil {
		return nil, fmt.Errorf("HTTP 路由必须声明 read/config/admin 最低权限并提供处理器")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := ""
		if minimum != ScopeRead {
			tracked := &statusWriter{ResponseWriter: w}
			w = tracked
			defer func() {
				status := tracked.status
				if status == 0 {
					status = 200
				}
				a.logger.Info("API privileged request", "tokenName", name, "method", r.Method, "path", r.URL.Path, "status", status)
			}()
		}
		w.Header().Set("Cache-Control", "no-store")
		if err := a.CheckHost(r); err != nil {
			writeError(w, 403, "forbidden_host", err.Error())
			return
		}
		principal, found, err := a.authenticate(r)
		if err != nil {
			a.logger.Error("API token lookup failed", "error", err)
			writeError(w, 500, "internal_error", "读取访问令牌失败，请检查服务日志")
			return
		}
		if !found {
			a.unauthorized(w)
			return
		}
		name = principal.Name
		w.Header().Set("X-Lgmon-Scope", string(principal.Scope))
		if principal.Guest {
			w.Header().Set("X-Lgmon-Guest", "1")
		}
		if principal.Guest || principal.LANFull {
			if ip, ok := lanVisitorRequest(r); ok {
				a.visitors.Touch(ip)
			}
		}
		if !principal.Scope.Allows(minimum) {
			writeError(w, 403, "insufficient_scope", fmt.Sprintf("这个操作需要 %s 权限，当前令牌只有 %s。请以管理员身份运行 lgmon token create --scope %s", minimum, principal.Scope, minimum))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
	}), nil
}
