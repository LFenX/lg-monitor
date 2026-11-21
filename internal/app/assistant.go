// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
)

const (
	// assistantRounds bounds how many times one question may go back to the model after tool calls. Sorting out a
	// board's dependencies took 8 rounds of looking things up before the first change, so the bound is wide; the
	// turn's time limit and the context budget keep a runaway in check.
	assistantRounds = 40
	// assistantResultLimit keeps a single tool result from crowding out the conversation.
	assistantResultLimit = 48 * 1024
	assistantName        = "波西米亚"
)

func (b *APIBackend) assistantSetup() string {
	return fmt.Sprintf("& %s assistant set --home %s", quotePowerShell(b.options.Executable), quotePowerShell(b.options.Home))
}

func (b *APIBackend) secret(ctx context.Context, names ...string) string {
	for _, name := range names {
		value, err := b.options.Secrets.Get(ctx, name)
		key := strings.TrimSpace(string(value))
		clear(value)
		if err == nil && key != "" {
			return key
		}
	}
	return ""
}

// assistantTarget is DeepSeek with the saved key, or why it cannot be used yet.
func (b *APIBackend) assistantTarget(ctx context.Context, settings config.AssistantSettings) (assistantTarget, error) {
	target := assistantTarget{baseURL: firstOf(b.options.DeepSeekURL, config.DeepSeekURL), model: settings.EffectiveModel(), key: b.secret(ctx, config.DeepSeekKeyRef)}
	if target.key == "" {
		// Without a key of its own, 波西米亚 may talk through a colleague who shares theirs.
		if shared := b.sharedModel(ctx); shared != nil {
			return assistantTarget{baseURL: shared.Address + "/team/v1/assistant", model: shared.Model, shared: shared,
				client: &http.Client{Timeout: 4 * time.Minute, Transport: &http.Transport{TLSClientConfig: shared.Config, Proxy: nil, IdleConnTimeout: time.Minute}}}, nil
		}
		return target, &api.Fault{Status: 409, Code: "assistant_not_configured", Message: assistantName + "还没有 DeepSeek 的 API Key。到“设置 → " + assistantName + "”粘贴密钥，或让 Agent 帮你配置。"}
	}
	return target, nil
}

func (b *APIBackend) dashScope(ctx context.Context, settings config.AssistantSettings) (dashScope, error) {
	client := dashScope{base: firstOf(b.options.DashScopeURL, settings.DashScopeURL()), key: b.secret(ctx, config.DashScopeKeyRef, config.QwenKeyRef), embedding: settings.EffectiveEmbeddingModel(), rerank: settings.EffectiveRerankModel(), client: &http.Client{Timeout: time.Minute}}
	if client.key == "" {
		return client, &api.Fault{Status: 409, Code: "assistant_not_configured", Message: assistantName + "还没有百炼的 API Key（向量模型和 rerank 用）。"}
	}
	return client, nil
}

// deepSeekModels lists the models this DeepSeek account may use; it also proves the key works.
func deepSeekModels(ctx context.Context, base, key string) ([]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return nil, networkFailure(err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode >= 300 {
		return nil, statusFailure(response.StatusCode, fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(payload[:min(len(payload), 400)]))))
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &list); err != nil {
		return nil, &api.Fault{Status: 502, Code: "assistant_upstream", Message: "DeepSeek 返回的模型列表无法识别", Cause: err}
	}
	models := []string{}
	for _, item := range list.Data {
		if item.ID != "" {
			models = append(models, item.ID)
		}
	}
	slices.Sort(models)
	return models, nil
}

// assistantModels remembers the account's model list for a while; the settings page and agents read it often.
type assistantModels struct {
	mu     sync.Mutex
	at     time.Time
	key    string
	models []string
}

func (b *APIBackend) knownModels(ctx context.Context, key string) []string {
	b.models.mu.Lock()
	defer b.models.mu.Unlock()
	if key == "" {
		return nil
	}
	if b.models.key == key && time.Since(b.models.at) < 10*time.Minute {
		return b.models.models
	}
	lookup, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	models, err := deepSeekModels(lookup, firstOf(b.options.DeepSeekURL, config.DeepSeekURL), key)
	if err != nil {
		return b.models.models
	}
	b.models.at, b.models.key, b.models.models = time.Now(), key, models
	return models
}

func (b *APIBackend) assistantStatus(ctx context.Context, cfg *config.Config) api.AssistantStatus {
	settings := cfg.Settings.Assistant
	deepseek := b.secret(ctx, config.DeepSeekKeyRef)
	status := api.AssistantStatus{
		Name: assistantName, Model: settings.EffectiveModel(), Region: settings.EffectiveRegion(),
		EmbeddingModel: settings.EffectiveEmbeddingModel(), RerankModel: settings.EffectiveRerankModel(),
		DeepSeekKey: deepseek != "", DashScopeKey: b.secret(ctx, config.DashScopeKeyRef, config.QwenKeyRef) != "",
		Models: b.knownModels(ctx, deepseek), Docs: b.helpIndexed(settings), Setup: b.assistantSetup(),
	}
	if status.Models == nil {
		status.Models = []string{}
	}
	status.Configured = status.DeepSeekKey && status.DashScopeKey
	if !status.DeepSeekKey {
		if shared := b.sharedModel(ctx); shared != nil {
			// A colleague's model answers; without 百炼 here the library is searched by keywords.
			share := shared.AssistantShare
			status.SharedBy, status.Model, status.Configured = &share, shared.Model, true
			return status
		}
	}
	switch {
	case !status.DeepSeekKey && !status.DashScopeKey:
		status.Problem = "还没有配置：需要 DeepSeek 的 API Key（回答问题）和百炼的 API Key（查找说明用的向量模型和 rerank）。"
	case !status.DeepSeekKey:
		status.Problem = "还差 DeepSeek 的 API Key（回答问题用）。"
	case !status.DashScopeKey:
		status.Problem = "还差百炼的 API Key（查找说明用的向量模型和 rerank）。"
	}
	return status
}

// configureAssistant checks each part with one small request before anything is saved, so a wrong key or
// model is reported here rather than in the middle of a conversation. Keys are only ever written.
func (b *APIBackend) configureAssistant(ctx context.Context, cfg *config.Config, r api.Request) (any, error) {
	var input api.AssistantConfigInput
	if err := api.DecodeBody(r.Body, &input); err != nil {
		return nil, err
	}
	deepseekKey, dashscopeKey := strings.TrimSpace(input.DeepSeekKey), strings.TrimSpace(input.DashScopeKey)
	input.DeepSeekKey, input.DashScopeKey = "", ""
	settings := cfg.Settings.Assistant
	if settings.Provider != "" && settings.Provider != "deepseek" {
		settings.Model = ""
	}
	settings.Provider, settings.BaseURL, settings.KeyRef = "", "", ""
	if input.Model != nil {
		settings.Model = strings.TrimSpace(*input.Model)
	}
	if input.Region != nil {
		settings.Region = strings.TrimSpace(*input.Region)
	}
	if input.EmbeddingModel != nil {
		settings.EmbeddingModel = strings.TrimSpace(*input.EmbeddingModel)
	}
	if input.RerankModel != nil {
		settings.RerankModel = strings.TrimSpace(*input.RerankModel)
	}
	if err := settings.Check(); err != nil {
		return nil, invalidAPI(err.Error())
	}
	probe, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	failed := func(part string, err error) error {
		var fault *api.Fault
		if errors.As(err, &fault) {
			return &api.Fault{Status: 400, Code: fault.Code, Message: "没有保存：" + part + "：" + fault.Message}
		}
		return invalidAPI("没有保存：" + part + "：连接失败")
	}
	// DeepSeek: the model list proves the key, and the chosen model must be on it.
	chatChanged := deepseekKey != "" || input.Model != nil
	if chatChanged {
		key := firstOf(deepseekKey, b.secret(ctx, config.DeepSeekKeyRef))
		if key == "" {
			return nil, invalidAPI("请先粘贴 DeepSeek 的 API Key")
		}
		models, err := deepSeekModels(probe, firstOf(b.options.DeepSeekURL, config.DeepSeekURL), key)
		if err != nil {
			return nil, failed("DeepSeek", err)
		}
		if settings.Model != "" && len(models) > 0 && !slices.Contains(models, settings.Model) {
			return nil, invalidAPI("没有保存：DeepSeek 账号里没有模型 " + settings.Model + "，可选：" + strings.Join(models, "、"))
		}
		if settings.Model == "" && len(models) > 0 && !slices.Contains(models, config.DefaultDeepSeekModel) {
			settings.Model = models[0]
		}
		b.models.mu.Lock()
		b.models.at, b.models.key, b.models.models = time.Now(), key, models
		b.models.mu.Unlock()
	}
	// 百炼: one embedding and one tiny rerank prove the key, the region and both models.
	searchChanged := dashscopeKey != "" || input.Region != nil || input.EmbeddingModel != nil || input.RerankModel != nil
	if searchChanged {
		client := dashScope{base: firstOf(b.options.DashScopeURL, settings.DashScopeURL()), key: firstOf(dashscopeKey, b.secret(ctx, config.DashScopeKeyRef, config.QwenKeyRef)), embedding: settings.EffectiveEmbeddingModel(), rerank: settings.EffectiveRerankModel(), client: &http.Client{Timeout: 30 * time.Second}}
		if client.key == "" {
			return nil, invalidAPI("请先粘贴百炼的 API Key")
		}
		if _, err := client.embed(probe, []string{"lgmon"}); err != nil {
			return nil, failed("百炼向量模型", err)
		}
		if _, err := client.rerankScores(probe, "lgmon", []string{"lgmon 是监控工具", "天气不错"}); err != nil {
			return nil, failed("百炼 rerank 模型", err)
		}
	}
	for ref, key := range map[string]string{config.DeepSeekKeyRef: deepseekKey, config.DashScopeKeyRef: dashscopeKey} {
		if key == "" {
			continue
		}
		if err := b.options.Secrets.Set(ctx, ref, []byte(key)); err != nil {
			return nil, &api.Fault{Status: 500, Code: "secret_save_failed", Message: "加密保存 API Key 失败，请检查服务日志", Cause: err}
		}
	}
	if _, err := b.options.Editor.Update(ctx, "settings.yaml", func(old []byte) ([]byte, error) {
		return config.SetAssistant(old, settings)
	}); err != nil {
		return nil, err
	}
	// Once 百炼 works, the guides and every service card are embedded in the background, before the first question.
	b.refreshKnowledge()
	return b.assistantStatus(ctx, b.options.Config.Snapshot()), nil
}

func firstOf(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type assistantTool struct {
	name, description, label string
	properties               map[string]any
	required                 []string
	// schema, when set, is a complete MCP input schema used instead of properties and required.
	schema map[string]any
	// mcp tools run through the toolbox; the rest run here.
	mcp, readOnly, confirm bool
	run                    func(ctx context.Context, cfg *config.Config, input map[string]any) (any, error)
}

func text(input map[string]any, name string) string {
	value, _ := input[name].(string)
	return strings.TrimSpace(value)
}

// assistantTools is lgmon's own guide search followed by every MCP tool the page's session may offer 波西米亚.
func (b *APIBackend) assistantTools(toolbox api.AssistantToolbox) []assistantTool {
	tools := []assistantTool{
		{name: "search_help", label: assistantLabel("search_help"), readOnly: true, description: "在知识库里查找：lgmon 自己的使用说明（怎么添加监控、子服务和面板是什么、告警为什么要等 1 分钟、飞书和内网共享怎么用等），这里每个服务的资料（说明、角色、监控方式、访问入口、依赖谁、谁依赖它），以及服务文档（部署、启动、排障）。按意思和关键词一起检索。用户问 lgmon 本身怎么用、问某个服务是做什么的、怎么部署或排障时先调用，按返回的段落回答，引用时说出处。",
			properties: map[string]any{"q": map[string]any{"type": "string", "description": "用户的问题，原样或稍加整理"}}, required: []string{"q"},
			run: func(ctx context.Context, cfg *config.Config, input map[string]any) (any, error) {
				query := text(input, "q")
				if query == "" {
					return nil, invalidAPI("q 不能为空")
				}
				hits, note, err := b.searchHelp(ctx, cfg, query)
				if err != nil {
					return nil, err
				}
				result := map[string]any{"passages": hits}
				if note != "" {
					result["note"] = note
				}
				if len(hits) == 0 {
					result["note"] = "说明里没有找到相关内容；照实告诉用户，不要编造功能。"
				}
				return result, nil
			}},
	}
	tools = append(tools, assistantTool{name: "knowledge_docs", label: assistantLabel("knowledge_docs"), description: "设置哪些服务文档纳入向量检索（默认不纳入，只按关键词检索）。对方回答了要不要向量化文档后调用：all=true 全部、all=false 全部不要，或用 docs 逐个指定（服务 ID → true/false）。之后文档改动时只重新向量化改了的段落。",
		properties: map[string]any{
			"all":  map[string]any{"type": "boolean", "description": "true 全部纳入，false 全部不纳入；逐个指定时省略"},
			"docs": map[string]any{"type": "object", "description": "服务 ID 到 true/false", "additionalProperties": map[string]any{"type": "boolean"}},
		},
		run: func(ctx context.Context, cfg *config.Config, input map[string]any) (any, error) {
			var all *bool
			if value, ok := input["all"].(bool); ok {
				all = &value
			}
			choices := map[string]bool{}
			if docs, ok := input["docs"].(map[string]any); ok {
				for id, value := range docs {
					if on, ok := value.(bool); ok {
						if _, exists := cfg.Docs[id]; !exists {
							return nil, invalidAPI("服务 " + id + " 没有文档")
						}
						choices[id] = on
					}
				}
			}
			if err := b.setDocumentChoices(choices, all); err != nil {
				return nil, err
			}
			b.refreshKnowledge()
			return b.knowledgeState(ctx, b.options.Config.Snapshot()), nil
		}})
	if toolbox == nil {
		return tools
	}
	for _, tool := range toolbox.Tools() {
		tools = append(tools, assistantTool{name: tool.Name, description: tool.Description, label: assistantLabel(tool.Name), schema: tool.Schema, mcp: true, readOnly: tool.ReadOnly, confirm: tool.Confirm})
	}
	return tools
}

// assistantTurn is what one question knows: its tools, the names on screen, and how to report progress.
type assistantTurn struct {
	backend  *APIBackend
	tools    []assistantTool
	toolbox  api.AssistantToolbox
	emit     func(string, any) error
	monitors map[string]string
	boards   map[string]string
}

// AssistantChat answers one question. 波西米亚 reads and changes existing services through the MCP tools bound to
// the asking page's session; steps that delete, revoke or reach outside wait for the person to approve them.
func (b *APIBackend) AssistantChat(ctx context.Context, input api.AssistantInput, toolbox api.AssistantToolbox, emit func(string, any) error) error {
	if err := b.licensed(api.AssistantChat); err != nil {
		return err
	}
	cfg := b.options.Config.Snapshot()
	target, err := b.assistantTarget(ctx, cfg.Settings.Assistant)
	if err != nil {
		return err
	}
	if _, err := b.dashScope(ctx, cfg.Settings.Assistant); err != nil && target.shared == nil {
		return err
	}
	turn := &assistantTurn{backend: b, toolbox: toolbox, emit: emit, tools: b.assistantTools(toolbox), monitors: map[string]string{}, boards: map[string]string{}}
	monitors, _ := b.monitors(ctx, cfg)
	boards, _ := b.boardList(ctx, cfg)
	for _, monitor := range monitors {
		turn.monitors[monitor.ID] = monitor.Name
	}
	for _, board := range boards {
		turn.boards[board.ID] = board.Name
	}
	turn.tools = append(turn.tools, turn.openPage())
	system := assistantPersona + b.assistantSituation(ctx, cfg, input.Page, monitors, boards)
	session := newChatSession(target, system, turn.tools, input.Messages)
	say := func(text string) error { return emit("text", map[string]string{"delta": text}) }
	for round := 0; round < assistantRounds; round++ {
		stop, calls, err := session.Next(ctx, say)
		if err != nil {
			return err
		}
		switch stop {
		case stopRefusal:
			return say("\n\n这个问题我没法回答，换一种问法试试。")
		case stopLength:
			return say("\n\n（回答太长，被截断了。可以让我只说其中一部分。）")
		case stopEnd:
			return nil
		}
		results := make([]chatResult, 0, len(calls))
		for _, call := range calls {
			output, failed := turn.call(ctx, call)
			results = append(results, chatResult{ID: call.ID, Name: call.Name, Output: output, Failed: failed})
		}
		session.AddResults(results)
	}
	// Out of rounds: one last answer without tools says what was found, what was changed and what is left.
	if _, err := session.Final(ctx, say, assistantWrapUp); err != nil {
		return say("\n\n（这次能做的步数用完了，还没做完的部分可以让我接着做。）")
	}
	return nil
}

const assistantWrapUp = "这次能做的步数已经用完。不要再调用工具：直接告诉对方已经查清的、已经改好的，以及还没做完的具体是哪几项；对方说一句“接着做”你就能从这里继续。"

// openPage moves the asking page to another of lgmon's pages; the page follows the navigate event.
func (t *assistantTurn) openPage() assistantTool {
	return assistantTool{name: "open_page", label: assistantLabel("open_page"), readOnly: true,
		description: "把对方正在看的 lgmon 网页切到另一页。对方说带我去、打开、跳到、导航到某一页时调用，例如访问权限是 #/settings/access。page 只能用说明里列出的站内地址；面板和服务要用现在的 ID。切过去以后用一句话说到了哪里。",
		properties:  map[string]any{"page": map[string]any{"type": "string", "description": "站内地址，例如 #/settings/access、#/boards/<面板ID>、#/monitors/<服务ID>"}},
		required:    []string{"page"},
		run: func(_ context.Context, _ *config.Config, input map[string]any) (any, error) {
			hash, name, ok := sitePage(text(input, "page"), t.monitors, t.boards)
			if !ok {
				return nil, invalidAPI("没有这个页面：" + text(input, "page") + "。只能用说明里列出的站内地址，面板和服务用现在的 ID。")
			}
			if err := t.emit("navigate", map[string]string{"hash": hash}); err != nil {
				return nil, err
			}
			return map[string]string{"opened": name, "page": hash}, nil
		}}
}

func (t *assistantTurn) call(ctx context.Context, call chatCall) (string, bool) {
	var chosen *assistantTool
	for index := range t.tools {
		if t.tools[index].name == call.Name {
			chosen = &t.tools[index]
		}
	}
	if chosen == nil {
		return "没有名为 " + call.Name + " 的工具", true
	}
	input := map[string]any{}
	raw := strings.TrimSpace(string(call.Input))
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return "工具参数不是有效的 JSON 对象", true
	}
	subject, href := assistantSubject(chosen.name, input, t.monitors, t.boards)
	report := func(state string) {
		_ = t.emit("tool", map[string]any{"id": call.ID, "name": chosen.name, "label": chosen.label, "detail": subject, "state": state, "change": !chosen.readOnly})
	}
	if chosen.confirm {
		id, wait := t.toolbox.Approval()
		_ = t.emit("confirm", map[string]any{"id": id, "call": call.ID, "name": chosen.name, "label": chosen.label, "subject": subject,
			"consequence": assistantConsequences[chosen.name], "arguments": input})
		approved := wait(ctx)
		_ = t.emit("confirmed", map[string]any{"id": id, "approved": approved})
		if !approved {
			report("declined")
			return "用户没有同意，这一步没有执行。不要再次尝试，除非用户明确要求。", true
		}
	}
	report("running")
	toolCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var output string
	failed := false
	if chosen.mcp {
		output, failed = t.toolbox.Call(toolCtx, chosen.name, json.RawMessage(raw))
	} else {
		value, err := chosen.run(toolCtx, t.backend.options.Config.Snapshot(), input)
		if err != nil {
			failed, output = true, "查询失败"
			var fault *api.Fault
			if errors.As(err, &fault) {
				output = fault.Message
			}
		} else if encoded, err := json.Marshal(value); err != nil {
			failed, output = true, "结果无法编码"
		} else {
			output = string(encoded)
		}
	}
	if failed {
		report("failed")
		return output, true
	}
	report("done")
	if !chosen.readOnly {
		_ = t.emit("change", map[string]any{"id": call.ID, "name": chosen.name, "label": chosen.label, "subject": subject, "href": href})
	}
	if len(output) > assistantResultLimit {
		// Say so explicitly; a silently clipped result reads as complete data.
		return "结果超过了一次能读取的大小，没有返回任何内容。请用 q、state、limit、LIMIT 或更短的时间范围缩小查询后重试。", true
	}
	return output, false
}

// sharedModel is a colleague's model this computer may use, if it is paired with one who shares it.
func (b *APIBackend) sharedModel(ctx context.Context) *SharedModel {
	if b.options.Teams == nil {
		return nil
	}
	shared, err := b.options.Teams.SharedModel(ctx)
	if err != nil {
		return nil
	}
	return shared
}

// ModelOffer and ForwardChat are this computer's side of sharing its model: only with its own key, and always
// with the model chosen here, whatever the request names.
func (b *APIBackend) ModelOffer(ctx context.Context) (string, bool) {
	settings := b.options.Config.Snapshot().Settings.Assistant
	return settings.EffectiveModel(), b.secret(ctx, config.DeepSeekKeyRef) != ""
}

func (b *APIBackend) ForwardChat(ctx context.Context, body []byte, w http.ResponseWriter) error {
	settings := b.options.Config.Snapshot().Settings.Assistant
	key := b.secret(ctx, config.DeepSeekKeyRef)
	if key == "" {
		return &api.Fault{Status: 404, Code: "assistant_not_shared", Message: "对方还没有配置自己的模型"}
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		return invalidAPI("模型请求格式无效")
	}
	request["model"] = settings.EffectiveModel()
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	outgoing, err := http.NewRequestWithContext(ctx, http.MethodPost, firstOf(b.options.DeepSeekURL, config.DeepSeekURL)+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return err
	}
	outgoing.Header.Set("Content-Type", "application/json")
	outgoing.Header.Set("Accept", "text/event-stream")
	outgoing.Header.Set("Authorization", "Bearer "+key)
	response, err := (&http.Client{Timeout: 4 * time.Minute}).Do(outgoing)
	if err != nil {
		return &api.Fault{Status: 502, Code: "assistant_unavailable", Message: "对方连不上 DeepSeek"}
	}
	defer response.Body.Close()
	if content := response.Header.Get("Content-Type"); content != "" {
		w.Header().Set("Content-Type", content)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(response.StatusCode)
	flusher, _ := w.(http.Flusher)
	buffer := make([]byte, 16*1024)
	for {
		n, readErr := response.Body.Read(buffer)
		if n > 0 {
			if _, err := w.Write(buffer[:n]); err != nil {
				return nil
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return nil
		}
	}
}
