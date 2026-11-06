package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/invopop/jsonschema"
)

func (Match) JSONSchemaExtend(schema *jsonschema.Schema) {
	for _, field := range []string{"process", "cmdline", "port", "service"} {
		schema.AnyOf = append(schema.AnyOf, &jsonschema.Schema{Required: []string{field}})
	}
}

func (Probe) JSONSchemaExtend(schema *jsonschema.Schema) {
	schema.AllOf = []*jsonschema.Schema{
		{If: discriminator("type", "http"), Then: &jsonschema.Schema{Required: []string{"url"}}},
		{If: discriminator("type", "tcp"), Then: &jsonschema.Schema{Required: []string{"address"}}},
	}
}

func (Template) JSONSchemaExtend(schema *jsonschema.Schema) {
	schema.OneOf = []*jsonschema.Schema{{Required: []string{"extends"}}, {Required: []string{"raw"}}}
}

func discriminator(field, value string) *jsonschema.Schema {
	return &jsonschema.Schema{Extras: map[string]any{"properties": map[string]any{field: map[string]any{"const": value}}}, Required: []string{field}}
}

func Schemas() (map[string][]byte, error) {
	models := map[string]any{
		"targets.schema.json":       TargetsFile{},
		"rules.schema.json":         RulesFile{},
		"notifiers.schema.json":     NotifiersFile{},
		"card-template.schema.json": Template{},
	}
	result := make(map[string][]byte, len(models))
	for name, model := range models {
		reflector := jsonschema.Reflector{Anonymous: true}
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
