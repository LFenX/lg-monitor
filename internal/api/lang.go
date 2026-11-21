package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type uiLangStore struct {
	mu   sync.RWMutex
	lang string
	file string
}

func normalizeUILang(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "en":
		return "en"
	default:
		return "zh"
	}
}

func newUILangStore(file string) *uiLangStore {
	store := &uiLangStore{lang: "zh", file: file}
	if file != "" {
		if raw, err := os.ReadFile(file); err == nil {
			store.lang = normalizeUILang(string(raw))
		}
	}
	return store
}

func (u *uiLangStore) get() string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.lang
}

func (u *uiLangStore) set(lang string) error {
	clean := strings.ToLower(strings.TrimSpace(lang))
	if clean != "zh" && clean != "en" {
		return fmt.Errorf("不支持的界面语言：%s", lang)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.file != "" {
		if err := os.MkdirAll(filepath.Dir(u.file), 0o700); err != nil {
			return fmt.Errorf("创建语言偏好目录失败：%w", err)
		}
		tmp := u.file + ".tmp"
		if err := os.WriteFile(tmp, []byte(clean), 0o600); err != nil {
			return fmt.Errorf("保存界面语言失败：%w", err)
		}
		if err := os.Rename(tmp, u.file); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("保存界面语言失败：%w", err)
		}
	}
	u.lang = clean
	return nil
}

type uiLangPayload struct {
	Lang string `json:"lang"`
}

func (s *Server) handleUILang(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSONForRequest(w, r, 200, uiLangPayload{Lang: s.uiLang.get()})
	case http.MethodPut:
		if principal, ok := CurrentPrincipal(r.Context()); ok && principal.Guest {
			writeError(w, 403, "guest_read_only", "内网访客只读查看，不能修改本机界面语言")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		if err != nil {
			writeError(w, 400, "invalid_body", "读取请求体失败")
			return
		}
		var payload uiLangPayload
		if err := DecodeBody(body, &payload); err != nil {
			var fault *Fault
			if ok := asFault(err, &fault); ok {
				writeError(w, fault.Status, fault.Code, fault.Message)
				return
			}
			writeError(w, 400, "invalid_request", err.Error())
			return
		}
		if err := s.uiLang.set(payload.Lang); err != nil {
			writeError(w, 400, "invalid_lang", err.Error())
			return
		}
		lang := s.uiLang.get()
		_ = s.options.Broker.Publish("lang", uiLangPayload{Lang: lang})
		writeJSONForRequest(w, r, 200, uiLangPayload{Lang: lang})
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, 405, "method_not_allowed", "此接口只支持 GET 或 PUT")
	}
}

func asFault(err error, target **Fault) bool {
	if fault, ok := err.(*Fault); ok {
		*target = fault
		return true
	}
	return false
}
