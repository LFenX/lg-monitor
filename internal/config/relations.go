package config

import (
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

// Roles are what a service is, in the words people use; each has its own icon in the dependency view.
var Roles = []string{"web", "api", "app", "database", "cache", "queue", "storage", "tunnel", "gateway", "worker", "scheduler", "ai", "mcp", "external"}

// Relations are the ways one service uses another; each has its own line style in the dependency view.
var Relations = []string{"uses", "calls", "data", "via", "runs", "sends"}

const (
	// MaxDocBytes bounds one service document; a runbook fits, a pasted log does not.
	MaxDocBytes         = 256 * 1024
	MaxDescriptionRunes = 300
	MaxDependencies     = 20
	MaxDependencyNote   = 80
)

// validateRelations checks what a target says about itself for people: its role, description and dependencies.
func validateRelations(target Target, index int, targets map[string]Target, root func(string) location, issues *Errors) {
	at := func(parts ...any) location {
		return root("targets.yaml").at(append([]any{"targets", index}, parts...)...)
	}
	if target.Job != nil {
		validateJob(target, at, targets, issues)
	}
	if target.Role != "" && !slices.Contains(Roles, target.Role) {
		at("role").add(issues, "目标 %s 的 role 必须是 %s 之一", target.ID, strings.Join(Roles, "、"))
	}
	if utf8.RuneCountInString(target.Description) > MaxDescriptionRunes {
		at("description").add(issues, "目标 %s 的说明最多 %d 个字", target.ID, MaxDescriptionRunes)
	}
	if len(target.Depends) > MaxDependencies {
		at("depends").add(issues, "目标 %s 最多写 %d 个依赖", target.ID, MaxDependencies)
	}
	seen := map[string]bool{}
	for j, item := range target.Depends {
		loc := at("depends", j)
		switch {
		case item.Target == target.ID:
			loc.add(issues, "目标 %s 不能依赖它自己", target.ID)
		case targets[item.Target].ID == "":
			loc.add(issues, "目标 %s 依赖的 %s 不存在，请先添加那个服务或改正 ID", target.ID, item.Target)
		case seen[item.Target]:
			loc.add(issues, "目标 %s 对 %s 的依赖重复了，一个服务只写一次", target.ID, item.Target)
		}
		seen[item.Target] = true
		if item.Relation != "" && !slices.Contains(Relations, item.Relation) {
			loc.at("relation").add(issues, "依赖关系 relation 必须是 %s 之一", strings.Join(Relations, "、"))
		}
		if utf8.RuneCountInString(item.Note) > MaxDependencyNote {
			loc.at("note").add(issues, "依赖的补充说明最多 %d 个字", MaxDependencyNote)
		}
	}
}

// DocPath is where a service's Markdown document lives in the configuration directory.
func DocPath(id string) string { return "docs/" + id + ".md" }

// loadDocs reads docs/*.md. A document whose service was removed by hand stays on disk and is simply not shown.
func (c *Config) loadDocs(fsys fs.FS, issues *Errors) {
	entries, err := fs.ReadDir(fsys, "docs")
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			issues.add("docs", nil, "读取服务文档目录失败，请检查目录和访问权限", err)
		}
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.EqualFold(path.Ext(name), ".md") {
			continue
		}
		file, id := path.Join("docs", name), strings.TrimSuffix(name, path.Ext(name))
		if !idPattern.MatchString(id) {
			issues.add(file, nil, "服务文档的文件名必须是服务 ID 加 .md，例如 docs/site.md："+file, nil)
			continue
		}
		data, err := fs.ReadFile(fsys, file)
		switch {
		case err != nil:
			issues.add(file, nil, "读取服务文档失败："+file, err)
		case len(data) > MaxDocBytes:
			issues.add(file, nil, "服务文档不能超过 256 KiB："+file, nil)
		case !utf8.Valid(data):
			issues.add(file, nil, "服务文档必须是 UTF-8 文本："+file, nil)
		default:
			if c.Docs == nil {
				c.Docs = map[string]string{}
			}
			c.Docs[id] = strings.ReplaceAll(string(data), "\r\n", "\n")
		}
	}
}
