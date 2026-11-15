package config

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// AssistantSettings configures 波西米亚, the in-page assistant: DeepSeek writes the answers, 百炼 (DashScope)
// embeds and reranks lgmon's own guides so it can answer how things work. Keys live in the secret store.
type AssistantSettings struct {
	Model          string `yaml:"model,omitempty" json:"model,omitempty" jsonschema:"maxLength=200" jsonschema_description:"DeepSeek 对话模型，省略时用 deepseek-chat；可用的模型以 DeepSeek 账号里列出的为准。"`
	Region         string `yaml:"region,omitempty" json:"region,omitempty" jsonschema:"enum=beijing,enum=singapore" jsonschema_description:"百炼地域：beijing（默认，国内）或 singapore（国际）。"`
	EmbeddingModel string `yaml:"embeddingModel,omitempty" json:"embeddingModel,omitempty" jsonschema:"maxLength=200" jsonschema_description:"百炼向量模型，省略时用 qwen3.7-text-embedding。"`
	RerankModel    string `yaml:"rerankModel,omitempty" json:"rerankModel,omitempty" jsonschema:"maxLength=200" jsonschema_description:"百炼 rerank 模型，省略时用 qwen3.7-text-rerank。"`
	// Before U5 the assistant could talk to many vendors. These are still read so older files load; they no
	// longer change anything.
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty" jsonschema_description:"旧版字段，已不再使用：现在对话固定用 DeepSeek。"`
	BaseURL  string `yaml:"baseUrl,omitempty" json:"baseUrl,omitempty" jsonschema_description:"旧版字段，已不再使用。"`
	KeyRef   string `yaml:"keyRef,omitempty" json:"keyRef,omitempty" jsonschema_description:"旧版字段，已不再使用。"`
}

const (
	DeepSeekURL           = "https://api.deepseek.com"
	DefaultDeepSeekModel  = "deepseek-chat"
	DefaultEmbeddingModel = "qwen3.7-text-embedding"
	DefaultRerankModel    = "qwen3.7-text-rerank"
	// DeepSeekKeyRef is where an older DeepSeek setup kept its key, so it keeps working.
	DeepSeekKeyRef  = "assistant-deepseek-key"
	DashScopeKeyRef = "assistant-dashscope-key"
	// QwenKeyRef held a 百炼 key when Qwen was the chat provider; it serves embeddings and rerank just as well.
	QwenKeyRef = "assistant-qwen-key"
)

// EffectiveModel ignores a model left over from another vendor: it would not exist at DeepSeek.
func (s AssistantSettings) EffectiveModel() string {
	model := strings.TrimSpace(s.Model)
	if model == "" || (s.Provider != "" && s.Provider != "deepseek") {
		return DefaultDeepSeekModel
	}
	return model
}

func (s AssistantSettings) EffectiveRegion() string {
	if s.Region == "singapore" {
		return "singapore"
	}
	return "beijing"
}

// DashScopeURL is the 百炼 endpoint for the chosen region.
func (s AssistantSettings) DashScopeURL() string {
	if s.EffectiveRegion() == "singapore" {
		return "https://dashscope-intl.aliyuncs.com"
	}
	return "https://dashscope.aliyuncs.com"
}

func (s AssistantSettings) EffectiveEmbeddingModel() string {
	if model := strings.TrimSpace(s.EmbeddingModel); model != "" {
		return model
	}
	return DefaultEmbeddingModel
}

func (s AssistantSettings) EffectiveRerankModel() string {
	if model := strings.TrimSpace(s.RerankModel); model != "" {
		return model
	}
	return DefaultRerankModel
}

// Check reports why these settings cannot be used, without contacting anyone.
func (s AssistantSettings) Check() error {
	if s.Region != "" && s.Region != "beijing" && s.Region != "singapore" {
		return fmt.Errorf("assistant.region 必须是 beijing 或 singapore")
	}
	for name, value := range map[string]string{"model": s.Model, "embeddingModel": s.EmbeddingModel, "rerankModel": s.RerankModel} {
		if len(value) > 200 || strings.ContainsAny(value, " \t\r\n") {
			return fmt.Errorf("assistant.%s 必须是不含空格、最多 200 个字符的模型 ID", name)
		}
	}
	return nil
}

// SetAssistant replaces the assistant block of a settings document and keeps everything else as written.
// The retired vendor fields are dropped on the way, so a saved file only says what still matters.
func SetAssistant(data []byte, value AssistantSettings) ([]byte, error) {
	if err := value.Check(); err != nil {
		return nil, err
	}
	if value.Provider != "" && value.Provider != "deepseek" {
		value.Model = ""
	}
	value.Provider, value.BaseURL, value.KeyRef = "", "", ""
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
