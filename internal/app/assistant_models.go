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
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
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
// so whatever a provider returns (Claude's thinking blocks, for one) goes back unchanged.
type chatSession interface {
	Next(ctx context.Context, delta func(string) error) (stop string, calls []chatCall, err error)
	AddResults(results []chatResult)
}

type assistantTarget struct {
	settings config.AssistantSettings
	provider config.AssistantProvider
	key      string
}

func newChatSession(target assistantTarget, system string, tools []assistantTool, history []api.AssistantMessage) chatSession {
	if target.provider.Protocol == "anthropic" {
		return newClaudeSession(target, system, tools, history)
	}
	return newOpenAISession(target, system, tools, history)
}

// ---------- Claude, through the official SDK ----------

type claudeSession struct {
	client anthropic.Client
	params anthropic.BetaMessageNewParams
}

func newClaudeSession(target assistantTarget, system string, tools []assistantTool, history []api.AssistantMessage) *claudeSession {
	// Only the configured key and gateway apply: a login profile, ANTHROPIC_BASE_URL or custom
	// headers in the service environment must not silently redirect or re-authenticate the assistant.
	options := []option.RequestOption{option.WithoutEnvironmentDefaults(), option.WithAPIKey(target.key), option.WithMaxRetries(2)}
	official := target.settings.BaseURL == "" || strings.TrimRight(target.settings.BaseURL, "/") == target.provider.BaseURL
	if !official {
		options = append(options, option.WithBaseURL(target.settings.EffectiveBaseURL()))
	}
	session := &claudeSession{client: anthropic.NewClient(options...)}
	session.params = anthropic.BetaMessageNewParams{
		Model:     target.settings.EffectiveModel(),
		MaxTokens: 16000,
		System:    []anthropic.BetaTextBlockParam{{Text: system}},
	}
	if official {
		// A declined request is re-served by a model chosen for that refusal category; gateways may not know the beta.
		session.params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		session.params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}
	for _, tool := range tools {
		definition := anthropic.BetaToolParam{Name: tool.name, Description: anthropic.String(tool.description), InputSchema: anthropic.BetaToolInputSchemaParam{Properties: tool.properties, Required: tool.required}}
		session.params.Tools = append(session.params.Tools, anthropic.BetaToolUnionParam{OfTool: &definition})
	}
	for _, message := range history {
		block := anthropic.NewBetaTextBlock(message.Text)
		if message.Role == "assistant" {
			session.params.Messages = append(session.params.Messages, anthropic.BetaMessageParam{OfAssistant: &anthropic.BetaAssistantMessageParam{Content: []anthropic.BetaContentBlockParamUnion{block}}})
		} else {
			session.params.Messages = append(session.params.Messages, anthropic.NewBetaUserMessage(block))
		}
	}
	return session
}

func (s *claudeSession) Next(ctx context.Context, delta func(string) error) (string, []chatCall, error) {
	stream := s.client.Beta.Messages.NewStreaming(ctx, s.params)
	message := anthropic.BetaMessage{}
	for stream.Next() {
		event := stream.Current()
		if err := message.Accumulate(event); err != nil {
			return "", nil, claudeFailure(err)
		}
		if change, ok := event.AsAny().(anthropic.BetaRawContentBlockDeltaEvent); ok {
			if value, ok := change.Delta.AsAny().(anthropic.BetaTextDelta); ok && value.Text != "" {
				if err := delta(value.Text); err != nil {
					return "", nil, err
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		return "", nil, claudeFailure(err)
	}
	switch message.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return stopRefusal, nil, nil
	case anthropic.BetaStopReasonMaxTokens:
		return stopLength, nil, nil
	case anthropic.BetaStopReasonToolUse:
	default:
		return stopEnd, nil, nil
	}
	s.params.Messages = append(s.params.Messages, message.ToParam())
	var calls []chatCall
	for _, block := range message.Content {
		if call, ok := block.AsAny().(anthropic.BetaToolUseBlock); ok {
			calls = append(calls, chatCall{ID: call.ID, Name: call.Name, Input: json.RawMessage(call.JSON.Input.Raw())})
		}
	}
	return stopTools, calls, nil
}

func (s *claudeSession) AddResults(results []chatResult) {
	blocks := make([]anthropic.BetaContentBlockParamUnion, 0, len(results))
	for _, result := range results {
		blocks = append(blocks, anthropic.NewBetaToolResultBlock(result.ID, result.Output, result.Failed))
	}
	s.params.Messages = append(s.params.Messages, anthropic.NewBetaUserMessage(blocks...))
}

func claudeFailure(err error) error {
	var failure *anthropic.Error
	if errors.As(err, &failure) {
		return statusFailure(failure.StatusCode, err)
	}
	return networkFailure(err)
}

// ---------- every other provider, through the OpenAI chat-completions protocol ----------

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
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.target.settings.EffectiveBaseURL()+"/chat/completions", bytes.NewReader(data))
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
	body := map[string]any{"model": s.target.settings.EffectiveModel(), "messages": s.messages, "stream": true}
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

// ---------- errors shared by both ----------

func statusFailure(status int, err error) error {
	switch {
	case status == 401 || status == 403:
		return &api.Fault{Status: 502, Code: "assistant_key_rejected", Message: "模型服务拒绝了 API Key，请重新保存有效的密钥", Cause: err}
	case status == 404:
		return &api.Fault{Status: 502, Code: "assistant_model_missing", Message: "模型服务找不到这个模型或接口地址，请检查模型 ID 和 baseUrl", Cause: err}
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
	return &api.Fault{Status: 502, Code: "assistant_unreachable", Message: "连接不上模型服务，请检查网络、代理或接口地址", Cause: err}
}
