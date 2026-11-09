package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/invopop/jsonschema"
)

func (Dashboard) JSONSchemaExtend(schema *jsonschema.Schema) {
	schema.OneOf = []*jsonschema.Schema{
		{Required: []string{"panels"}, Not: &jsonschema.Schema{Required: []string{"html"}}},
		{Required: []string{"html"}, Not: &jsonschema.Schema{Required: []string{"panels"}}},
	}
}

func (CollectorMonitor) JSONSchemaExtend(schema *jsonschema.Schema) {
	if id, ok := schema.Properties.Get("id"); ok {
		id.Not = &jsonschema.Schema{Pattern: `^(default|con|prn|aux|nul|com[1-9]|lpt[1-9])$`}
	}
}

func (Match) JSONSchemaExtend(schema *jsonschema.Schema) {
	for _, field := range []string{"process", "cmdline", "port", "service", "exeDir", "workDir"} {
		schema.AnyOf = append(schema.AnyOf, &jsonschema.Schema{Required: []string{field}})
	}
	for _, name := range []string{"exeDir", "workDir"} {
		if field, ok := schema.Properties.Get(name); ok {
			field.Pattern = `^(?:[A-Za-z]:[\\/]|[\\/]{2}[^\\/:*?"<>|\x00-\x1f]+[\\/][^\\/:*?"<>|\x00-\x1f]+(?:[\\/]|$))[^:*?"<>|\x00-\x1f]*$`
		}
	}
	schema.AllOf = append(schema.AllOf, &jsonschema.Schema{
		If: &jsonschema.Schema{Required: []string{"workDir"}},
		Then: &jsonschema.Schema{AnyOf: []*jsonschema.Schema{
			{Required: []string{"process"}}, {Required: []string{"cmdline"}}, {Required: []string{"exeDir"}},
		}},
	})
}

func (Probe) JSONSchemaExtend(schema *jsonschema.Schema) {
	schema.AllOf = []*jsonschema.Schema{
		{If: discriminator("type", "http"), Then: &jsonschema.Schema{Required: []string{"url"}}},
		{If: discriminator("type", "tcp"), Then: &jsonschema.Schema{Required: []string{"address"}}},
		{If: discriminator("type", "script"), Then: &jsonschema.Schema{Required: []string{"script"}}},
	}
	schema.AllOf = append(schema.AllOf,
		&jsonschema.Schema{If: &jsonschema.Schema{Required: []string{"keywordAbsent"}}, Then: &jsonschema.Schema{Required: []string{"keyword"}}},
		&jsonschema.Schema{If: &jsonschema.Schema{Not: discriminator("type", "http")}, Then: &jsonschema.Schema{Not: &jsonschema.Schema{AnyOf: []*jsonschema.Schema{{Required: []string{"keyword"}}, {Required: []string{"keywordAbsent"}}}}}},
	)
	if keyword, ok := schema.Properties.Get("keyword"); ok {
		keyword.Extras = map[string]any{"x-maxUtf8Bytes": 256}
	}
	if name, ok := schema.Properties.Get("script"); ok {
		name.Pattern, name.Not = scriptNamePattern, &jsonschema.Schema{Pattern: `[.][.]`}
	}
	if args, ok := schema.Properties.Get("args"); ok && args.Items != nil {
		args.Items.Pattern = scriptArgumentPattern
	}
}

func (Template) JSONSchemaExtend(schema *jsonschema.Schema) {
	schema.OneOf = []*jsonschema.Schema{{Required: []string{"extends"}}, {Required: []string{"raw"}}}
}

func (Notifier) JSONSchemaExtend(schema *jsonschema.Schema) {
	for _, field := range []string{"urlRef", "secretRef"} {
		if property, ok := schema.Properties.Get(field); ok {
			property.Not = &jsonschema.Schema{Pattern: "://"}
		}
	}
	schema.AllOf = []*jsonschema.Schema{{If: discriminator("type", "feishu_app"), Then: &jsonschema.Schema{Required: []string{"app"}, OneOf: []*jsonschema.Schema{{Required: []string{"chatId"}}, {Required: []string{"openId"}}}}}}
	schema.AllOf = append(schema.AllOf, &jsonschema.Schema{If: discriminator("cardActions", true), Then: &jsonschema.Schema{Required: []string{"chatId"}}})
	for _, kind := range []string{"feishu_webhook", "webhook"} {
		schema.AllOf = append(schema.AllOf, &jsonschema.Schema{If: discriminator("type", kind), Then: &jsonschema.Schema{Required: []string{"urlRef"}}})
	}
	for _, field := range []string{"app", "chatId", "openId", "chatName", "template"} {
		schema.AllOf = append(schema.AllOf, &jsonschema.Schema{If: discriminator("type", "toast"), Then: &jsonschema.Schema{Not: &jsonschema.Schema{Required: []string{field}}}})
	}
	for _, field := range []string{"app", "chatId", "openId", "chatName"} {
		schema.AllOf = append(schema.AllOf, &jsonschema.Schema{If: discriminator("type", "feishu_webhook"), Then: &jsonschema.Schema{Not: &jsonschema.Schema{Required: []string{field}}}})
		schema.AllOf = append(schema.AllOf, &jsonschema.Schema{If: discriminator("type", "webhook"), Then: &jsonschema.Schema{Not: &jsonschema.Schema{Required: []string{field}}}})
	}
	schema.AllOf = append(schema.AllOf, &jsonschema.Schema{If: discriminator("type", "webhook"), Then: &jsonschema.Schema{Not: &jsonschema.Schema{Required: []string{"template"}}}})
	for _, kind := range []string{"feishu_app", "toast"} {
		for _, field := range []string{"urlRef", "secretRef"} {
			schema.AllOf = append(schema.AllOf, &jsonschema.Schema{If: discriminator("type", kind), Then: &jsonschema.Schema{Not: &jsonschema.Schema{Required: []string{field}}}})
		}
	}
}

func (NotifierFilter) JSONSchemaExtend(schema *jsonschema.Schema) {
	if field, ok := schema.Properties.Get("severity"); ok && field.Items != nil {
		field.Items.Enum = []any{"critical", "warning", "info"}
	}
	if field, ok := schema.Properties.Get("collectors"); ok && field.Items != nil {
		maximum := uint64(255)
		field.Items.Pattern, field.Items.MaxLength = `^[^\s*/\\:]+$`, &maximum
	}
	if field, ok := schema.Properties.Get("tags"); ok && field.Items != nil {
		minimum := uint64(1)
		field.Items.MinLength = &minimum
	}
}

func (Rule) JSONSchemaExtend(schema *jsonschema.Schema) {
	schema.AllOf = []*jsonschema.Schema{{
		If:   discriminator("type", "collector_offline"),
		Then: &jsonschema.Schema{Not: &jsonschema.Schema{Required: []string{"target"}}},
		Else: &jsonschema.Schema{If: &jsonschema.Schema{Not: &jsonschema.Schema{AnyOf: []*jsonschema.Schema{discriminator("type", "sql"), discriminator("type", "metric_threshold")}}}, Then: &jsonschema.Schema{Required: []string{"target"}}},
	}}
	schema.AllOf = append(schema.AllOf, &jsonschema.Schema{If: discriminator("type", "metric_threshold"), Then: &jsonschema.Schema{Required: []string{"metric", "op", "threshold"}}}, &jsonschema.Schema{If: discriminator("type", "sql"), Then: &jsonschema.Schema{Required: []string{"query"}}})
	schema.AllOf = append(schema.AllOf,
		&jsonschema.Schema{If: discriminator("type", "cert_expiry"), Then: &jsonschema.Schema{Not: &jsonschema.Schema{Required: []string{"for"}}}},
		&jsonschema.Schema{If: &jsonschema.Schema{Not: discriminator("type", "cert_expiry")}, Then: &jsonschema.Schema{Not: &jsonschema.Schema{Required: []string{"within"}}}},
	)
}

func discriminator(field string, value any) *jsonschema.Schema {
	return &jsonschema.Schema{Extras: map[string]any{"properties": map[string]any{field: map[string]any{"const": value}}}, Required: []string{field}}
}

func Schemas() (map[string][]byte, error) {
	models := map[string]any{
		"targets.schema.json":         TargetsFile{},
		"rules.schema.json":           RulesFile{},
		"notifiers.schema.json":       NotifiersFile{},
		"card-template.schema.json":   Template{},
		"dashboard.schema.json":       Dashboard{},
		"settings.schema.json":        Settings{},
		"webhook-payload.schema.json": WebhookPayload{},
	}
	result := make(map[string][]byte, len(models))
	for name, model := range models {
		reflector := jsonschema.Reflector{Anonymous: true}
		if name == "webhook-payload.schema.json" {
			reflector.AllowAdditionalProperties = true
		}
		schema := reflector.Reflect(model)
		data, err := json.MarshalIndent(schema, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("生成 %s 失败：%w", name, err)
		}
		result[name] = append(data, '\n')
	}
	return result, nil
}

func WriteSchemas(directory string) ([]string, error) {
	schemas, err := Schemas()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("创建 Schema 目录失败：%w", err)
	}
	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(directory, name), schemas[name], 0o644); err != nil {
			return nil, fmt.Errorf("写入 %s 失败：%w", name, err)
		}
	}
	return names, nil
}
