package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/invopop/jsonschema"
)

func modelSchema(model any) (map[string]any, error) {
	if models, ok := model.(alternativeModels); ok {
		choices := []any{}
		for _, item := range models {
			schema, err := modelSchema(item)
			if err != nil {
				return nil, err
			}
			choices = append(choices, schema)
		}
		return map[string]any{"oneOf": choices}, nil
	}
	if model == nil {
		return map[string]any{}, nil
	}
	reflector := jsonschema.Reflector{Anonymous: true, DoNotReference: true}
	data, err := json.Marshal(reflector.Reflect(model))
	if err != nil {
		return nil, fmt.Errorf("生成 HTTP 模型说明失败：%w", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("读取 HTTP 模型说明失败：%w", err)
	}
	delete(schema, "$schema")
	return schema, nil
}

func openAPIDocument(routes []Route) (map[string]any, error) {
	paths := map[string]any{}
	errorSchema := map[string]any{"type": "object", "required": []string{"error"}, "properties": map[string]any{"error": map[string]any{"type": "object", "required": []string{"code", "message"}, "properties": map[string]any{"code": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"}, "issues": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}}}}}
	for _, route := range routes {
		response, err := modelSchema(route.Response)
		if err != nil {
			return nil, err
		}
		contentType := "application/json"
		if route.ID == Stream {
			contentType = "text/event-stream"
			response = map[string]any{"type": "string"}
		}
		responses := map[string]any{"200": map[string]any{"description": "成功", "content": map[string]any{contentType: map[string]any{"schema": response}}}}
		for _, status := range []string{"400", "401", "403", "404", "405", "408", "409", "413", "422", "429", "500", "502", "503", "504"} {
			responses[status] = map[string]any{"description": "错误；message 为中文说明", "content": map[string]any{"application/json": map[string]any{"schema": errorSchema}}}
		}
		operation := map[string]any{"operationId": route.ID, "summary": route.Summary, "x-lgmon-scope": route.Scope, "security": []any{map[string]any{"bearerAuth": []string{}}}, "responses": responses}
		parameters := []any{}
		for _, field := range strings.Split(route.Path, "/") {
			if strings.HasPrefix(field, "{") {
				parameters = append(parameters, map[string]any{"name": strings.Trim(field, "{}"), "in": "path", "required": true, "schema": map[string]any{"type": "string", "minLength": 1}})
			}
		}
		for _, query := range route.Query {
			schema := map[string]any{"type": "string"}
			if query.Name == "from" || query.Name == "to" || query.Name == "since" {
				schema = map[string]any{"type": "integer", "format": "int64", "minimum": 0}
			}
			if query.Name == "step" {
				schema["enum"] = []string{"raw", "1m", "1h"}
			}
			if query.Name == "state" {
				schema["enum"] = []string{"firing", "resolved", "pending", "all"}
			}
			parameters = append(parameters, map[string]any{"name": query.Name, "in": "query", "description": query.Description, "schema": schema})
		}
		if len(parameters) != 0 {
			operation["parameters"] = parameters
		}
		if route.Request != nil {
			request, err := modelSchema(route.Request)
			if err != nil {
				return nil, err
			}
			if route.ID == PutFeishuApp {
				properties := request["properties"].(map[string]any)
				properties["secret"].(map[string]any)["writeOnly"] = true
			}
			content := map[string]any{"application/json": map[string]any{"schema": request}}
			if route.ID == PutTargetsConfig || route.ID == PutRulesConfig || route.ID == PutNotifiersConfig || route.ID == PutSettingsConfig || route.ID == PutTemplate {
				content["application/yaml"] = map[string]any{"schema": request}
			}
			operation["requestBody"] = map[string]any{"required": route.ID != TestRule && route.ID != PreviewTemplate && route.ID != TestNotifier && route.ID != ValidateConfig, "content": content}
		}
		if paths[route.Path] == nil {
			paths[route.Path] = map[string]any{}
		}
		paths[route.Path].(map[string]any)[strings.ToLower(route.Method)] = operation
	}
	return map[string]any{"openapi": "3.1.0", "info": map[string]any{"title": "lg-monitor HTTP API", "version": "v1"}, "paths": paths, "components": map[string]any{"securitySchemes": map[string]any{"bearerAuth": map[string]any{"type": "http", "scheme": "bearer"}}}}, nil
}
