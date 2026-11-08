package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

const MaxRequestBody = 1024 * 1024
const MaxValidationRequestBody = 25 * MaxRequestBody

type Request struct {
	Params      map[string]string
	Query       url.Values
	Body        []byte
	ContentType string
}

type Action func(context.Context, Request) (any, error)

type Fault struct {
	Status        int
	Code, Message string
	Cause         error
}

func (e *Fault) Error() string { return e.Message }
func (e *Fault) Unwrap() error { return e.Cause }

func DecodeBody(body []byte, value any) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return &Fault{400, "invalid_request", "请求体必须是符合接口格式的 JSON 对象", nil}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return &Fault{400, "invalid_request", "请求体必须是符合接口格式的 JSON 对象", err}
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return &Fault{400, "invalid_request", "请求体只能包含一个 JSON 对象", err}
	}
	return nil
}

type ServerOptions struct {
	Version     string
	Auth        *Authenticator
	Actions     map[Operation]Action
	Broker      *Broker
	Now         func() time.Time
	Logger      *slog.Logger
	StreamClock StreamClock
	Frontend    http.Handler
}

type Server struct {
	options ServerOptions
	mux     *http.ServeMux
	spec    map[string]any
	replay  chan struct{}
}

func NewServer(opts ServerOptions) (*Server, error) { return newServer(opts, Routes()) }

func newServer(opts ServerOptions, routes []Route) (*Server, error) {
	if opts.Auth == nil || opts.Broker == nil || opts.Now == nil {
		return nil, fmt.Errorf("HTTP API 需要认证、实时事件和时钟依赖")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.StreamClock == nil {
		opts.StreamClock = nativeStreamClock{}
	}
	opts.Actions = maps.Clone(opts.Actions)
	s := &Server{options: opts, mux: http.NewServeMux(), replay: make(chan struct{}, 1)}
	groups := map[string]map[string]http.Handler{}
	ids := map[Operation]bool{}
	for _, route := range routes {
		if route.ID == "" || ids[route.ID] || !route.Scope.Valid() || route.Path == "" || !slices.Contains([]string{"GET", "POST", "PUT", "DELETE"}, route.Method) {
			return nil, fmt.Errorf("HTTP 路由 %s 必须有唯一编号、有效方法和最低权限", route.ID)
		}
		ids[route.ID] = true
		if route.ID != OpenAPI && route.ID != Stream && opts.Actions[route.ID] == nil {
			return nil, fmt.Errorf("HTTP 路由 %s 缺少处理器", route.ID)
		}
		protected, err := opts.Auth.Protect(route.Scope, s.handle(route))
		if err != nil {
			return nil, err
		}
		if groups[route.Path] == nil {
			groups[route.Path] = map[string]http.Handler{}
		}
		if groups[route.Path][route.Method] != nil {
			return nil, fmt.Errorf("HTTP 路由 %s 重复注册", route.Path)
		}
		groups[route.Path][route.Method] = protected
	}
	for pattern, methods := range groups {
		s.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if handler := methods[r.Method]; handler != nil {
				handler.ServeHTTP(w, r)
				return
			}
			allowed := make([]string, 0, len(methods))
			for method := range methods {
				allowed = append(allowed, method)
			}
			slices.Sort(allowed)
			if err := opts.Auth.CheckHost(r); err != nil {
				writeError(w, 403, "forbidden_host", err.Error())
				return
			}
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			writeError(w, 405, "method_not_allowed", "此接口不支持该请求方法")
		})
	}
	var err error
	s.spec, err = openAPIDocument(routes)
	if err != nil {
		return nil, err
	}
	if err := s.registerMCP(); err != nil {
		return nil, err
	}
	s.mux.HandleFunc("/api/v1/session", func(w http.ResponseWriter, r *http.Request) {
		token, local, err := opts.Auth.LocalSession(r)
		if err != nil || !local {
			writeError(w, 403, "local_only", "会话接口只允许无代理的回环地址和本机 Host")
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, 405, "method_not_allowed", "此接口只支持 GET")
			return
		}
		w.Header().Set("X-Lgmon-Scope", string(ScopeRead))
		writeJSON(w, 200, SessionResult{Token: token, Scope: ScopeRead})
	})
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if err := opts.Auth.CheckHost(r); err != nil {
			writeError(w, 403, "forbidden_host", err.Error())
			return
		}
		if opts.Frontend != nil && !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api" {
			opts.Frontend.ServeHTTP(w, r)
			return
		}
		writeError(w, 404, "not_found", "接口不存在，请查看 /api/v1/openapi.json")
	})
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != path.Clean(r.URL.Path) {
		if err := s.options.Auth.CheckHost(r); err != nil {
			writeError(w, 403, "forbidden_host", err.Error())
			return
		}
		writeError(w, 400, "invalid_path", "请求路径不能包含多余斜杠或相对路径")
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handle(route Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				s.options.Logger.Error("API handler panicked", "operation", route.ID)
				writeError(w, 500, "internal_error", "接口执行失败，请检查服务日志")
			}
		}()
		if route.ID == OpenAPI {
			writeJSON(w, 200, s.spec)
			return
		}
		if route.ID == Stream {
			s.stream(w, r)
			return
		}
		request := Request{Params: map[string]string{}, Query: r.URL.Query(), ContentType: r.Header.Get("Content-Type")}
		for _, field := range strings.Split(route.Path, "/") {
			if strings.HasPrefix(field, "{") {
				name := strings.Trim(field, "{}")
				request.Params[name] = r.PathValue(name)
			}
		}
		if r.Body != nil {
			var err error
			limit := int64(MaxRequestBody)
			if route.ID == ValidateConfig {
				limit = MaxValidationRequestBody
			}
			request.Body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
			if err != nil {
				var sizeError *http.MaxBytesError
				if errors.As(err, &sizeError) {
					writeError(w, 413, "request_too_large", fmt.Sprintf("请求体不能超过 %d MiB", limit/MaxRequestBody))
				} else {
					writeError(w, 400, "invalid_body", "读取请求体失败，请重新发送完整请求")
				}
				return
			}
		}
		if route.ID == TestRule {
			select {
			case s.replay <- struct{}{}:
				defer func() { <-s.replay }()
			default:
				writeError(w, 429, "replay_busy", "已有规则回放正在运行，请稍后重试")
				return
			}
		}
		result, err := s.options.Actions[route.ID](r.Context(), request)
		if err != nil {
			s.failure(w, route.ID, err)
			return
		}
		if route.ID == PutNotificationPause || route.ID == DeleteNotificationPause {
			if err := s.options.Broker.Publish("pause", result); err != nil {
				s.options.Logger.Warn("Pause event publication failed")
			}
		}
		writeJSON(w, 200, result)
	})
}

func (s *Server) failure(w http.ResponseWriter, operation Operation, err error) {
	var fault *Fault
	if errors.As(err, &fault) && fault.Status >= 400 && fault.Status <= 599 && fault.Code != "" {
		if fault.Status == 429 && fault.Code == "notification_rate_limited" {
			w.Header().Set("Retry-After", "10")
		}
		if fault.Status == 429 && (fault.Code == "process_rate_limited" || fault.Code == "service_switch_rate_limited") {
			w.Header().Set("Retry-After", "2")
		}
		var issues config.Errors
		if errors.As(err, &issues) {
			writeJSON(w, fault.Status, map[string]any{"error": map[string]any{"code": fault.Code, "message": fault.Message, "issues": issues}})
			return
		}
		writeError(w, fault.Status, fault.Code, fault.Message)
		return
	}
	s.options.Logger.Error("API operation failed", "operation", operation)
	writeError(w, 500, "internal_error", "接口执行失败，请检查服务日志")
}
