package app

import (
	"context"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
)

// roleWords guess a service's role from its name and what it watches, so the dependency view has sensible icons
// before anyone sets a role. Checked in order: "MCP 文档服务" is an MCP service, not storage.
var roleWords = []struct {
	role  string
	words []string
}{
	{"mcp", []string{"mcp"}},
	{"tunnel", []string{"tunnel", "隧道", "通道", "cloudflared", "frp", "ngrok", "穿透"}},
	{"database", []string{"postgres", "mysql", "mariadb", "mssql", "sqlserver", "mongo", "oracle", "sqlite", "clickhouse", "数据库"}},
	{"cache", []string{"redis", "memcache", "缓存"}},
	{"queue", []string{"rabbitmq", "kafka", "nats", "mqtt", "队列"}},
	{"gateway", []string{"nginx", "caddy", "traefik", "haproxy", "gateway", "网关", "代理", "proxy"}},
	{"ai", []string{"ollama", "vllm", "llm", "comfy", "模型"}},
	{"storage", []string{"minio", "存储", "备份", "storage", "ftp", "nas"}},
	{"scheduler", []string{"计划任务", "定时", "cron", "schedule"}},
}

// serviceRole is the role a person set, or a guess from the name, what it watches and its kind.
func serviceRole(target config.Target) (role string, guessed bool) {
	if target.Role != "" {
		return target.Role, false
	}
	text := strings.ToLower(strings.Join([]string{target.Name, target.ID, target.Match.Process, target.Match.Service, target.Task}, " "))
	for _, item := range roleWords {
		for _, word := range item.words {
			if strings.Contains(text, word) {
				return item.role, true
			}
		}
	}
	switch monitorKind(target) {
	case "url":
		return "web", true
	case "port":
		return "api", true
	case "service":
		return "worker", true
	case "task", "job":
		return "scheduler", true
	}
	return "app", true
}

func relation(item config.Dependency) string {
	if item.Relation == "" {
		return "uses"
	}
	return item.Relation
}

// dependencyViews lists every service's dependencies and, reversed, who depends on it.
func dependencyViews(cfg *config.Config) (depends, dependents map[string][]api.MonitorDependency) {
	depends, dependents = map[string][]api.MonitorDependency{}, map[string][]api.MonitorDependency{}
	for _, target := range cfg.Targets {
		for _, item := range target.Depends {
			depends[target.ID] = append(depends[target.ID], api.MonitorDependency{ID: item.Target, Relation: relation(item), Note: item.Note, Optional: item.Optional})
			dependents[item.Target] = append(dependents[item.Target], api.MonitorDependency{ID: target.ID, Relation: relation(item), Note: item.Note, Optional: item.Optional})
		}
	}
	return depends, dependents
}

// applyRelations sets what the caller passed and keeps the rest; nil means "not given".
func applyRelations(cfg *config.Config, target *config.Target, description, role *string, depends *[]config.Dependency) error {
	if description != nil {
		text := strings.TrimSpace(*description)
		if utf8.RuneCountInString(text) > config.MaxDescriptionRunes {
			return invalidAPI("说明最多 300 个字；更长的内容写进文档（put_monitor_doc）")
		}
		target.Description = text
	}
	if role != nil {
		value := strings.TrimSpace(*role)
		if value != "" && !slices.Contains(config.Roles, value) {
			return invalidAPI("role 必须是 " + strings.Join(config.Roles, "、") + " 之一，或空字符串表示按类型推断")
		}
		target.Role = value
	}
	if depends == nil {
		return nil
	}
	if len(*depends) > config.MaxDependencies {
		return invalidAPI("一个服务最多写 20 个依赖")
	}
	known := targetIndex(cfg)
	result := make([]config.Dependency, 0, len(*depends))
	for _, item := range *depends {
		item.Target, item.Relation, item.Note = strings.TrimSpace(item.Target), strings.TrimSpace(item.Relation), strings.TrimSpace(item.Note)
		switch {
		case item.Target == target.ID:
			return invalidAPI("服务不能依赖它自己")
		case known[item.Target].ID == "":
			return invalidAPI("依赖的服务 " + item.Target + " 不存在；先调用 list_monitors 核对 ID")
		case slices.ContainsFunc(result, func(other config.Dependency) bool { return other.Target == item.Target }):
			return invalidAPI("对 " + item.Target + " 的依赖写了两次，一个服务只写一次")
		case item.Relation != "" && !slices.Contains(config.Relations, item.Relation):
			return invalidAPI("relation 必须是 " + strings.Join(config.Relations, "、") + " 之一")
		case utf8.RuneCountInString(item.Note) > config.MaxDependencyNote:
			return invalidAPI("依赖的补充说明最多 80 个字")
		}
		if item.Relation == "uses" {
			item.Relation = ""
		}
		result = append(result, item)
	}
	target.Depends = result
	return nil
}

func (b *APIBackend) monitorDoc(cfg *config.Config, id string) (api.MonitorDoc, error) {
	if targetIndex(cfg)[id].ID == "" {
		return api.MonitorDoc{}, missingAPI("监控 " + id + " 不存在")
	}
	return api.MonitorDoc{ID: id, Markdown: cfg.Docs[id]}, nil
}

func (b *APIBackend) putMonitorDoc(ctx context.Context, cfg *config.Config, r api.Request) (api.MonitorDoc, error) {
	id := r.Params["id"]
	var input api.MonitorDocInput
	if err := api.DecodeBody(r.Body, &input); err != nil {
		return api.MonitorDoc{}, err
	}
	if targetIndex(cfg)[id].ID == "" {
		return api.MonitorDoc{}, missingAPI("监控 " + id + " 不存在")
	}
	if len(input.Markdown) > config.MaxDocBytes {
		return api.MonitorDoc{}, invalidAPI("文档不能超过 256 KiB；把日志、长表格换成要点和链接")
	}
	updated, err := b.options.Editor.SaveDoc(ctx, id, input.Markdown)
	if err != nil {
		return api.MonitorDoc{}, err
	}
	return api.MonitorDoc{ID: id, Markdown: updated.Docs[id]}, nil
}
