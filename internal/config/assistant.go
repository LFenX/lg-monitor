package config

import (
	"bytes"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// AssistantSettings selects the model behind the in-page assistant.
// The API key itself lives in the secret store under KeyRef.
type AssistantSettings struct {
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty" jsonschema:"enum=anthropic,enum=openai,enum=deepseek,enum=qwen,enum=moonshot,enum=zhipu,enum=doubao,enum=siliconflow,enum=openrouter,enum=gemini,enum=groq,enum=mistral,enum=xai,enum=ollama,enum=lmstudio,enum=custom" jsonschema_description:"模型供应商，省略为 anthropic；除 anthropic 外都按 OpenAI 兼容接口调用。"`
	Model    string `yaml:"model,omitempty" json:"model,omitempty" jsonschema:"maxLength=200" jsonschema_description:"模型 ID，省略时用供应商的推荐模型。"`
	BaseURL  string `yaml:"baseUrl,omitempty" json:"baseUrl,omitempty" jsonschema:"format=uri" jsonschema_description:"接口基础地址，省略时用供应商默认地址；custom 必填。"`
	KeyRef   string `yaml:"keyRef,omitempty" json:"keyRef,omitempty" jsonschema:"pattern=^[a-z0-9-]+$" jsonschema_description:"密钥库中 API Key 的名称，省略时按供应商生成。"`
}

// AssistantProvider describes one model vendor the assistant can talk to.
type AssistantProvider struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Protocol string   `json:"protocol" jsonschema:"enum=anthropic,enum=openai"`
	BaseURL  string   `json:"baseUrl"`
	Model    string   `json:"model" jsonschema_description:"推荐模型；供应商更新较快，可以改成任意可用模型。"`
	Models   []string `json:"models"`
	NeedsKey bool     `json:"needsKey"`
	KeyURL   string   `json:"keyUrl,omitempty" jsonschema_description:"申请 API Key 的页面。"`
	Region   string   `json:"region" jsonschema:"enum=global,enum=china,enum=local"`
}

// AssistantProviders is the catalog shown to people and agents. Every entry except anthropic speaks
// the OpenAI chat-completions protocol, which is what most vendors and local runtimes expose.
var AssistantProviders = []AssistantProvider{
	{ID: "anthropic", Name: "Anthropic Claude", Protocol: "anthropic", BaseURL: "https://api.anthropic.com", Model: "claude-opus-5-5", Models: []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-2"}, NeedsKey: true, KeyURL: "https://console.anthropic.com/settings/keys", Region: "global"},
	{ID: "openai", Name: "OpenAI", Protocol: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-5", Models: []string{"gpt-5", "gpt-5-mini", "gpt-4.1"}, NeedsKey: true, KeyURL: "https://platform.openai.com/api-keys", Region: "global"},
	{ID: "deepseek", Name: "DeepSeek 深度求索", Protocol: "openai", BaseURL: "https://api.deepseek.com/v1", Model: "deepseek-chat", Models: []string{"deepseek-chat", "deepseek-reasoner"}, NeedsKey: true, KeyURL: "https://platform.deepseek.com/api_keys", Region: "china"},
	{ID: "qwen", Name: "通义千问 Qwen", Protocol: "openai", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", Model: "qwen-plus", Models: []string{"qwen-plus", "qwen-max", "qwen-turbo"}, NeedsKey: true, KeyURL: "https://bailian.console.aliyun.com/", Region: "china"},
	{ID: "moonshot", Name: "Kimi 月之暗面", Protocol: "openai", BaseURL: "https://api.moonshot.cn/v1", Model: "kimi-latest", Models: []string{"kimi-latest", "moonshot-v1-32k"}, NeedsKey: true, KeyURL: "https://platform.moonshot.cn/console/api-keys", Region: "china"},
	{ID: "zhipu", Name: "智谱 GLM", Protocol: "openai", BaseURL: "https://open.bigmodel.cn/api/paas/v4", Model: "glm-4-plus", Models: []string{"glm-4-plus", "glm-4-flash"}, NeedsKey: true, KeyURL: "https://open.bigmodel.cn/usercenter/apikeys", Region: "china"},
	{ID: "doubao", Name: "豆包 火山方舟", Protocol: "openai", BaseURL: "https://ark.cn-beijing.volces.com/api/v3", Model: "", Models: []string{}, NeedsKey: true, KeyURL: "https://console.volcengine.com/ark", Region: "china"},
	{ID: "siliconflow", Name: "硅基流动 SiliconFlow", Protocol: "openai", BaseURL: "https://api.siliconflow.cn/v1", Model: "deepseek-ai/DeepSeek-V3", Models: []string{"deepseek-ai/DeepSeek-V3", "Qwen/Qwen2.5-72B-Instruct"}, NeedsKey: true, KeyURL: "https://cloud.siliconflow.cn/account/ak", Region: "china"},
	{ID: "openrouter", Name: "OpenRouter", Protocol: "openai", BaseURL: "https://openrouter.ai/api/v1", Model: "openai/gpt-5", Models: []string{"openai/gpt-5", "anthropic/claude-sonnet-5-5", "deepseek/deepseek-chat"}, NeedsKey: true, KeyURL: "https://openrouter.ai/keys", Region: "global"},
	{ID: "gemini", Name: "Google Gemini", Protocol: "openai", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai", Model: "gemini-2.5-flash", Models: []string{"gemini-2.5-flash", "gemini-2.5-pro"}, NeedsKey: true, KeyURL: "https://aistudio.google.com/apikey", Region: "global"},
	{ID: "groq", Name: "Groq", Protocol: "openai", BaseURL: "https://api.groq.com/openai/v1", Model: "llama-3.3-70b-versatile", Models: []string{"llama-3.3-70b-versatile"}, NeedsKey: true, KeyURL: "https://console.groq.com/keys", Region: "global"},
	{ID: "mistral", Name: "Mistral", Protocol: "openai", BaseURL: "https://api.mistral.ai/v1", Model: "mistral-large-latest", Models: []string{"mistral-large-latest", "mistral-small-latest"}, NeedsKey: true, KeyURL: "https://console.mistral.ai/api-keys", Region: "global"},
	{ID: "xai", Name: "xAI Grok", Protocol: "openai", BaseURL: "https://api.x.ai/v1", Model: "grok-4", Models: []string{"grok-4"}, NeedsKey: true, KeyURL: "https://console.x.ai/", Region: "global"},
	{ID: "ollama", Name: "Ollama（本机）", Protocol: "openai", BaseURL: "http://127.0.0.1:11434/v1", Model: "qwen3", Models: []string{"qwen3", "llama3.1", "deepseek-r1"}, NeedsKey: false, Region: "local"},
	{ID: "lmstudio", Name: "LM Studio（本机）", Protocol: "openai", BaseURL: "http://127.0.0.1:1234/v1", Model: "", Models: []string{}, NeedsKey: false, Region: "local"},
	{ID: "custom", Name: "自定义（OpenAI 兼容）", Protocol: "openai", BaseURL: "", Model: "", Models: []string{}, NeedsKey: false, Region: "global"},
}

// FindAssistantProvider returns the catalog entry for an ID; an empty ID means anthropic.
func FindAssistantProvider(id string) (AssistantProvider, bool) {
	if id == "" {
		id = "anthropic"
	}
	index := slices.IndexFunc(AssistantProviders, func(item AssistantProvider) bool { return item.ID == id })
	if index < 0 {
		return AssistantProvider{}, false
	}
	return AssistantProviders[index], true
}

func (s AssistantSettings) EffectiveProvider() string {
	if s.Provider == "" {
		return "anthropic"
	}
	return s.Provider
}

func (s AssistantSettings) EffectiveModel() string {
	if s.Model != "" {
		return s.Model
	}
	provider, _ := FindAssistantProvider(s.Provider)
	return provider.Model
}

func (s AssistantSettings) EffectiveBaseURL() string {
	if s.BaseURL != "" {
		return strings.TrimRight(s.BaseURL, "/")
	}
	provider, _ := FindAssistantProvider(s.Provider)
	return provider.BaseURL
}

// EffectiveKeyRef keeps the original name for Claude, so a key saved before providers existed still works,
// and gives every other provider its own name, so switching back and forth keeps each key.
func (s AssistantSettings) EffectiveKeyRef() string {
	if s.KeyRef != "" {
		return s.KeyRef
	}
	if provider := s.EffectiveProvider(); provider != "anthropic" {
		return "assistant-" + provider + "-key"
	}
	return "assistant-api-key"
}

// Check reports why these settings cannot be used, without contacting the provider.
func (s AssistantSettings) Check() error {
	provider, known := FindAssistantProvider(s.Provider)
	if !known {
		names := make([]string, 0, len(AssistantProviders))
		for _, item := range AssistantProviders {
			names = append(names, item.ID)
		}
		return fmt.Errorf("assistant.provider 必须是 %s 之一", strings.Join(names, "、"))
	}
	if address := s.EffectiveBaseURL(); address == "" {
		return fmt.Errorf("%s 需要填写接口地址 baseUrl", provider.Name)
	} else if parsed, err := url.Parse(address); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("assistant.baseUrl 必须是完整的 http:// 或 https:// 地址，不能包含账号密码")
	}
	if s.EffectiveModel() == "" {
		return fmt.Errorf("%s 需要填写模型 ID", provider.Name)
	}
	if len(s.Model) > 200 {
		return fmt.Errorf("assistant.model 不能超过 200 个字符")
	}
	if s.KeyRef != "" && !idPattern.MatchString(s.KeyRef) {
		return fmt.Errorf("assistant.keyRef 只能包含小写字母、数字和横线")
	}
	return nil
}

// SetAssistant replaces the assistant block of a settings document and keeps everything else as written.
func SetAssistant(data []byte, value AssistantSettings) ([]byte, error) {
	if err := value.Check(); err != nil {
		return nil, err
	}
	return setSettingsSection(data, "assistant", value)
}

// SetLAN replaces the lan section of settings.yaml and keeps everything else as written.
func SetLAN(data []byte, value LANSettings) ([]byte, error) {
	return setSettingsSection(data, "lan", value)
}

func setSettingsSection(data []byte, key string, value any) ([]byte, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("解析 settings.yaml 失败：%w", err)
		}
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("settings.yaml 的根节点必须是对象")
	}
	var encoded yaml.Node
	if err := encoded.Encode(value); err != nil {
		return nil, fmt.Errorf("编码 %s 设置失败：%w", key, err)
	}
	replaced := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			encoded.HeadComment = root.Content[i+1].HeadComment
			root.Content[i+1], replaced = &encoded, true
		}
	}
	if !replaced {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &encoded)
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, fmt.Errorf("编码 settings.yaml 失败：%w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("完成 settings.yaml 编码失败：%w", err)
	}
	if _, err := ParseSettings(out.Bytes()); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
