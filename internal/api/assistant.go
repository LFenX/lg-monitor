package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// assistantSlots bounds concurrent conversations, because every turn spends the owner's model quota.
var assistantSlots = make(chan struct{}, 2)

func (s *Server) assistant(w http.ResponseWriter, r *http.Request, request Request) {
	if s.options.Assistant == nil {
		writeError(w, 503, "assistant_unavailable", "AI 助手在这个角色上不可用")
		return
	}
	var input AssistantInput
	if err := DecodeBody(request.Body, &input); err != nil {
		s.failure(w, AssistantChat, err)
		return
	}
	if len(input.Messages) == 0 || len(input.Messages) > 40 || input.Messages[len(input.Messages)-1].Role != "user" {
		writeError(w, 400, "invalid_request", "messages 需要 1～40 条，且最后一条必须是 user")
		return
	}
	for _, message := range input.Messages {
		if (message.Role != "user" && message.Role != "assistant") || strings.TrimSpace(message.Text) == "" || utf8.RuneCountInString(message.Text) > 8000 {
			writeError(w, 400, "invalid_request", "每条消息的 role 必须是 user 或 assistant，text 为 1～8000 个字符")
			return
		}
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "internal_error", "当前连接不支持流式响应")
		return
	}
	select {
	case assistantSlots <- struct{}{}:
		defer func() { <-assistantSlots }()
	default:
		writeError(w, 429, "assistant_busy", "AI 助手正在回答其他问题，请稍后再试")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	var mu sync.Mutex
	emit := func(event string, data any) error {
		encoded, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("编码助手事件失败：%w", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, encoded); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if err := s.options.Assistant(ctx, input, emit); err != nil {
		message := "AI 助手暂时无法回答，请检查服务日志"
		var fault *Fault
		if errors.As(err, &fault) {
			message = fault.Message
		} else if ctx.Err() == nil {
			s.options.Logger.Error("Assistant turn failed", "error", err)
		}
		_ = emit("error", map[string]string{"message": message})
		return
	}
	_ = emit("done", map[string]bool{"ok": true})
}
