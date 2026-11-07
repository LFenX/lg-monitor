package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/invopop/jsonschema"
)

const MaxDraftFiles = 64
const MaxDraftFileBytes = 1024 * 1024
const MaxDraftTotalBytes = 4 * MaxDraftFileBytes

type ValidationInput struct {
	Files DraftFiles `json:"files,omitempty" jsonschema:"maxProperties=64" jsonschema_description:"相对配置路径到 YAML/JSON 内容的映射；null 假设删除；省略或空对象校验磁盘。"`
}

type DraftFiles map[string]*string

func (DraftFiles) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "object", AdditionalProperties: &jsonschema.Schema{OneOf: []*jsonschema.Schema{{Type: "string"}, {Type: "null"}}}}
}

type ValidationFile struct {
	Path   string `json:"path"`
	Source string `json:"source" jsonschema:"enum=disk,enum=draft,enum=deleted"`
}

type ValidationResult struct {
	Valid  bool             `json:"valid"`
	Issues Errors           `json:"issues"`
	Files  []ValidationFile `json:"files"`
}

func ValidateDrafts(drafts map[string]*string) error {
	if len(drafts) > MaxDraftFiles {
		return fmt.Errorf("草稿最多包含 64 个文件")
	}
	total := 0
	for name, content := range drafts {
		if !validDraftPath(name) {
			return fmt.Errorf("草稿路径 %q 无效，只允许四个主配置及 card-templates/<名字>.yaml、dashboards/<名字>.yaml，不能带 ..", name)
		}
		if content == nil {
			continue
		}
		if len(*content) > MaxDraftFileBytes {
			return fmt.Errorf("草稿文件 %s 不能超过 1 MiB", name)
		}
		total += len(*content)
		if total > MaxDraftTotalBytes {
			return fmt.Errorf("草稿文件内容合计不能超过 4 MiB")
		}
	}
	return nil
}

func validDraftPath(name string) bool {
	if !validDocumentPath(name) || strings.Contains(name, "..") {
		return false
	}
	if editableDocument(name) {
		return true
	}
	parts := strings.Split(name, "/")
	return len(parts) == 2 && (parts[0] == "card-templates" || parts[0] == "dashboards") &&
		strings.HasSuffix(parts[1], ".yaml") && len(parts[1]) > len(".yaml")
}

// ValidateFiles uses the loader's overlay without publishing or writing a snapshot.
func ValidateFiles(files fs.FS, drafts map[string]*string) (ValidationResult, error) {
	result := ValidationResult{Issues: Errors{}, Files: []ValidationFile{}}
	if files == nil {
		return result, fmt.Errorf("配置校验需要文件系统")
	}
	if err := ValidateDrafts(drafts); err != nil {
		return result, err
	}
	sources := map[string]string{}
	for _, name := range []string{"targets.yaml", "rules.yaml", "notifiers.yaml", "settings.yaml"} {
		if _, err := fs.Stat(files, name); !errors.Is(err, fs.ErrNotExist) {
			sources[name] = "disk"
		}
	}
	for _, directory := range []string{"card-templates", "dashboards"} {
		entries, _ := fs.ReadDir(files, directory)
		for _, entry := range entries {
			if !entry.IsDir() && (strings.EqualFold(path.Ext(entry.Name()), ".yaml") || strings.EqualFold(path.Ext(entry.Name()), ".yml")) {
				sources[path.Join(directory, entry.Name())] = "disk"
			}
		}
	}
	names := make([]string, 0, len(drafts))
	for name := range drafts {
		names = append(names, name)
	}
	slices.Sort(names)
	overlay := files
	for _, name := range names {
		content := drafts[name]
		item := documentOverlay{FS: overlay, name: name, remove: content == nil}
		sources[name] = "deleted"
		if content != nil {
			item.data = []byte(*content)
			sources[name] = "draft"
		}
		overlay = item
	}
	names = names[:0]
	for name := range sources {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		result.Files = append(result.Files, ValidationFile{Path: name, Source: sources[name]})
	}
	loaded, err := Load(overlay)
	if err != nil {
		if !errors.As(err, &result.Issues) {
			return result, err
		}
	} else {
		result.Issues = append(result.Issues, loaded.DashboardIssues()...)
	}
	result.Valid = len(result.Issues) == 0
	return result, nil
}

func ValidateTargetMatch(spec Match) error {
	return Validate(&Config{Targets: []Target{{ID: "process-query", Match: spec}}})
}
