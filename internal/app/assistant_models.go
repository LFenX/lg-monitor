package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
)

// chatCall is one tool request from the model, in a form every protocol can produce.
type chatCall struct {
	ID, Name string
	Input    json.RawMessage
}

type chatResult struct {
	ID, Name, Output string
	Failed           bool
}

const (
	stopEnd     = "end"
	stopTools   = "tools"
	stopLength  = "length"
	stopRefusal = "refusal"
)

// chatSession keeps one question's conversation in the provider's own message format,
// so whatever the model returns goes back unchanged.
type chatSession interface {
	Next(ctx context.Context, delta func(string) error) (stop string, calls []chatCall, err error)
	AddResults(results []chatResult)
}

// assistantTarget is the chat endpoint, model and key one conversation uses.
type assistantTarget struct {
	baseURL, model, key string
}

func newChatSession(target assistantTarget, system string, tools []assistantTool, history []api.AssistantMessage) chatSession {
	return newOpenAISession(target, system, tools, history)
}

// ---------- DeepSeek, through its OpenAI-compatible chat completions ----------

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    *string          `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAISession struct {
	target   assistantTarget
	client   *http.Client
	tools    []map[string]any
	messages []openAIMessage
}

func text2(value string) *string { return &value }

func newOpenAISession(target assistantTarget, system string, tools []assistantTool, history []api.AssistantMessage) *openAISession {
	session := &openAISession{target: target, client: &http.Client{Timeout: 4 * time.Minute}}
	for _, tool := range tools {
		required := tool.required
		if required == nil {
			required = []string{}
		}
		session.tools = append(session.tools, map[string]any{"type": "function", "function": map[string]any{
			"name": tool.name, "description": tool.description,
			"parameters": map[string]any{"type": "object", "properties": tool.properties, "required": required},
		}})
	}
	session.messages = append(session.messages, openAIMessage{Role: "system", Content: text2(system)})
	for _, message := range history {
		session.messages = append(session.messages, openAIMessage{Role: message.Role, Content: text2(message.Text)})
	}
	return session
}

func (s *openAISession) request(ctx context.Context, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("编码模型请求失败：%w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.target.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("创建模型请求失败：%w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if s.target.key != "" {
		request.Header.Set("Authorization", "Bearer "+s.target.key)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return nil, networkFailure(err)
	}
	if response.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		response.Body.Close()
		return nil, statusFailure(response.StatusCode, fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(detail))))
	}
	return response, nil
}

func (s *openAISession) Next(ctx context.Context, delta func(string) error) (string, []chatCall, error) {
	body := map[string]any{"model": s.target.model, "messages": s.messages, "stream": true}
	if len(s.tools) > 0 {
		body["tools"] = s.tools
	}
	response, err := s.request(ctx, body)
	if err != nil {
		return "", nil, err
	}
	defer response.Body.Close()
	var content strings.Builder
	calls := map[int]*openAIToolCall{}
	order := []int{}
	finish := ""
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Error != nil {
			return "", nil, &api.Fault{Status: 502, Code: "assistant_upstream", Message: "模型服务返回错误：" + chunk.Error.Message}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				content.WriteString(choice.Delta.Content)
				if err := delta(choice.Delta.Content); err != nil {
					return "", nil, err
				}
			}
			for _, part := range choice.Delta.ToolCalls {
				call := calls[part.Index]
				if call == nil {
					call = &openAIToolCall{Type: "function"}
					calls[part.Index] = call
					order = append(order, part.Index)
				}
				if part.ID != "" {
					call.ID = part.ID
				}
				call.Function.Name += part.Function.Name
				call.Function.Arguments += part.Function.Arguments
			}
			if choice.FinishReason != "" {
				finish = choice.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", nil, networkFailure(err)
	}
	if len(calls) == 0 {
		if finish == "length" {
			return stopLength, nil, nil
		}
		if finish == "content_filter" {
			return stopRefusal, nil, nil
		}
		return stopEnd, nil, nil
	}
	assistant := openAIMessage{Role: "assistant"}
	if content.Len() > 0 {
		assistant.Content = text2(content.String())
	}
	var result []chatCall
	for i, index := range order {
		call := calls[index]
		if call.ID == "" {
			// Some local runtimes omit IDs; the reply still has to name which call it answers.
			call.ID = fmt.Sprintf("call_%d", i)
		}
		if strings.TrimSpace(call.Function.Arguments) == "" {
			call.Function.Arguments = "{}"
		}
		assistant.ToolCalls = append(assistant.ToolCalls, *call)
		result = append(result, chatCall{ID: call.ID, Name: call.Function.Name, Input: json.RawMessage(call.Function.Arguments)})
	}
	s.messages = append(s.messages, assistant)
	return stopTools, result, nil
}

func (s *openAISession) AddResults(results []chatResult) {
	for _, result := range results {
		s.messages = append(s.messages, openAIMessage{Role: "tool", ToolCallID: result.ID, Content: text2(result.Output)})
	}
}

// ---------- errors ----------

func statusFailure(status int, err error) error {
	switch {
	case status == 401 || status == 403:
		return &api.Fault{Status: 502, Code: "assistant_key_rejected", Message: "DeepSeek 拒绝了这个 API Key，请到 DeepSeek 开放平台复制有效的密钥", Cause: err}
	case status == 404:
		return &api.Fault{Status: 502, Code: "assistant_model_missing", Message: "DeepSeek 找不到这个模型，请在设置里换一个账号可用的模型", Cause: err}
	case status == 429:
		return &api.Fault{Status: 502, Code: "assistant_rate_limited", Message: "模型服务暂时限流或额度不足，请稍后再试", Cause: err}
	case status >= 500:
		return &api.Fault{Status: 502, Code: "assistant_upstream", Message: "模型服务暂时不可用，请稍后再试", Cause: err}
	}
	return &api.Fault{Status: 502, Code: "assistant_request_rejected", Message: "模型服务没有接受这次请求（HTTP " + fmt.Sprint(status) + "）：请检查模型 ID 是否支持工具调用", Cause: err}
}

func networkFailure(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &api.Fault{Status: 504, Code: "assistant_timeout", Message: "这次回答超时了，请把问题问得更具体一些", Cause: err}
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return &api.Fault{Status: 502, Code: "assistant_unreachable", Message: "连接不上 DeepSeek，请检查网络或代理", Cause: err}
}
