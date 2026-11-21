package api

import (
	"bytes"
	"compress/gzip"
	"context"
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
)

type Principal struct {
	Name  string `json:"name"`
	Scope Scope  `json:"scope"`
	Local bool   `json:"local,omitempty"`
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

type AuthOptions struct {
	Tokens  TokenLookup
	Entropy io.Reader
	Hosts   *HostPolicy
	Logger  *slog.Logger
}
type Authenticator struct {
	tokens     TokenLookup
	local      Token
	localScope atomic.Value
	hosts      atomic.Pointer[HostPolicy]
	logger     *slog.Logger
}

func NewAuthenticator(opts AuthOptions) (*Authenticator, error) {
	if opts.Tokens == nil || opts.Hosts == nil {
		return nil, fmt.Errorf("HTTP API 认证需要令牌查询接口和主机白名单")
	}
	local, err := NewToken(ScopeRead, "local-browser", opts.Entropy)
	if err != nil {
		return nil, err
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	auth := &Authenticator{tokens: opts.Tokens, local: local, logger: opts.Logger}
	auth.localScope.Store(ScopeRead)
	auth.hosts.Store(opts.Hosts)
	return auth, nil
}

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

func (a *Authenticator) CheckHost(r *http.Request) error { return a.hosts.Load().Check(r) }

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

func (a *Authenticator) authenticate(r *http.Request) (Principal, bool, error) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return Principal{}, false, nil
	}
	fields := strings.Fields(values[0])
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || len(fields[1]) > 512 {
		return Principal{}, false, nil
	}
	hash := TokenHash(fields[1])
	if subtle.ConstantTimeCompare(hash[:], a.local.Digest[:]) == 1 {
		if !localRequest(r) {
			return Principal{}, false, nil
		}
		return Principal{Name: "local-browser", Scope: a.LocalScope(), Local: true}, true, nil
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
		if !principal.Scope.Allows(minimum) {
			writeError(w, 403, "insufficient_scope", fmt.Sprintf("这个操作需要 %s 权限，当前令牌只有 %s。请以管理员身份运行 lgmon token create --scope %s", minimum, principal.Scope, minimum))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
	}), nil
}
