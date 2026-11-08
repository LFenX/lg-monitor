package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type DocumentStore interface {
	fs.FS
	Replace(context.Context, string, []byte) error
	Remove(context.Context, string) error
}

type Editor struct {
	files   DocumentStore
	manager *Manager
	mu      sync.Mutex
}

func NewEditor(files DocumentStore, manager *Manager) (*Editor, error) {
	if files == nil || manager == nil {
		return nil, fmt.Errorf("配置编辑需要文件存储和配置管理器")
	}
	return &Editor{files: files, manager: manager}, nil
}

func editableDocument(name string) bool {
	return slices.Contains([]string{"targets.yaml", "rules.yaml", "notifiers.yaml", "settings.yaml"}, name)
}

func (e *Editor) Replace(ctx context.Context, name string, data []byte) (*Config, error) {
	return e.Update(ctx, name, func([]byte) ([]byte, error) { return data, nil })
}

func (e *Editor) Document(ctx context.Context, name string) ([]byte, error) {
	if !editableDocument(name) {
		return nil, fmt.Errorf("配置文件名无效")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("读取配置已取消：%w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	data, err := fs.ReadFile(e.files, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置原文失败：%w", err)
	}
	return data, nil
}

func (e *Editor) Validate(ctx context.Context, drafts map[string]*string) (ValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return ValidationResult{}, fmt.Errorf("配置校验已取消：%w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return ValidateFiles(e.files, drafts)
}

func (e *Editor) Update(ctx context.Context, name string, build func([]byte) ([]byte, error)) (*Config, error) {
	if !editableDocument(name) || build == nil {
		return nil, fmt.Errorf("只允许编辑 targets、rules、notifiers 或 settings 配置")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	old, err := fs.ReadFile(e.files, name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("读取待编辑配置失败：%w", err)
	}
	data, err := build(old)
	if err != nil {
		return nil, err
	}
	return e.apply(ctx, name, data, false)
}

func (e *Editor) SaveTemplate(ctx context.Context, id string, data []byte) (*Config, error) {
	if !idPattern.MatchString(id) || presets[id] {
		return nil, fmt.Errorf("请使用由小写字母、数字或横线组成的自定义模板 ID，预设模板不能覆盖")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.apply(ctx, "card-templates/"+id+".yaml", data, false)
}

func (e *Editor) DeleteTemplate(ctx context.Context, id string) (*Config, error) {
	if !idPattern.MatchString(id) || presets[id] {
		return nil, fmt.Errorf("请指定自定义模板 ID，预设模板不能删除")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	name := "card-templates/" + id + ".yaml"
	if _, err := fs.Stat(e.files, name); err != nil {
		return nil, fmt.Errorf("模板不存在或无法读取：%w", err)
	}
	return e.apply(ctx, name, nil, true)
}

func (e *Editor) apply(ctx context.Context, name string, data []byte, remove bool) (*Config, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("配置编辑已取消：%w", err)
	}
	previous := e.manager.Snapshot()
	if _, err := Load(documentOverlay{FS: e.files, name: name, data: data, remove: remove}); err != nil {
		return nil, err
	}
	old, readErr := fs.ReadFile(e.files, name)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return nil, fmt.Errorf("读取原配置失败：%w", readErr)
	}
	var err error
	if remove {
		err = e.files.Remove(ctx, name)
	} else {
		err = e.files.Replace(ctx, name, data)
	}
	if err != nil {
		return nil, fmt.Errorf("保存配置文件失败：%w", err)
	}
	if err = e.manager.Reload(); err != nil {
		// A concurrent file edit can invalidate the full configuration after preflight.
		var rollback error
		if errors.Is(readErr, fs.ErrNotExist) {
			rollback = e.files.Remove(context.WithoutCancel(ctx), name)
		} else {
			rollback = e.files.Replace(context.WithoutCancel(ctx), name, old)
		}
		restore := e.manager.Apply(previous)
		if rollback != nil || restore != nil {
			return nil, fmt.Errorf("重新加载配置失败，恢复原文件或快照也失败：%w", errors.Join(err, rollback, restore))
		}
		return nil, fmt.Errorf("重新加载配置失败，已恢复原文件并保留旧配置：%w", err)
	}
	return e.manager.Snapshot(), nil
}

type documentOverlay struct {
	fs.FS
	name   string
	data   []byte
	remove bool
}

func (f documentOverlay) Open(name string) (fs.File, error) {
	if name == f.name {
		if f.remove {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		return &documentFile{Reader: bytes.NewReader(f.data), name: path.Base(name), size: int64(len(f.data))}, nil
	}
	if f.FS == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return f.FS.Open(name)
}

func (f documentOverlay) ReadDir(name string) ([]fs.DirEntry, error) {
	var entries []fs.DirEntry
	var err error
	if f.FS != nil {
		entries, err = fs.ReadDir(f.FS, name)
	} else {
		err = fs.ErrNotExist
	}
	if name != path.Dir(f.name) {
		return entries, err
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	entries = slices.DeleteFunc(slices.Clone(entries), func(entry fs.DirEntry) bool { return entry.Name() == path.Base(f.name) })
	if !f.remove {
		entries = append(entries, fs.FileInfoToDirEntry(documentInfo{name: path.Base(f.name), size: int64(len(f.data))}))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

type documentInfo struct {
	name string
	size int64
}

func (i documentInfo) Name() string     { return i.name }
func (i documentInfo) Size() int64      { return i.size }
func (documentInfo) Mode() fs.FileMode  { return 0o600 }
func (documentInfo) ModTime() time.Time { return time.Time{} }
func (documentInfo) IsDir() bool        { return false }
func (documentInfo) Sys() any           { return nil }

type documentFile struct {
	*bytes.Reader
	name string
	size int64
}

func (f *documentFile) Stat() (fs.FileInfo, error) {
	return documentInfo{name: f.name, size: f.size}, nil
}
func (*documentFile) Close() error { return nil }

func ParseRule(data []byte, base *Config) (Rule, error) {
	return LoadRuleFile(documentOverlay{name: "rule-draft.yaml", data: data}, "rule-draft.yaml", base)
}

func ParseNotifiers(data []byte) (NotifiersFile, error) {
	var document NotifiersFile
	var issues Errors
	readDocument(documentOverlay{name: "notifiers.yaml", data: data}, "notifiers.yaml", &document, map[string]*yaml.Node{}, &issues)
	if len(issues) > 0 {
		return document, issues
	}
	return document, nil
}

func ParseSettings(data []byte) (Settings, error) {
	return LoadSettings(documentOverlay{name: "settings.yaml", data: data})
}

func ParseTemplate(data []byte, base *Config) (Template, *Config, error) {
	if base == nil {
		return Template{}, nil, fmt.Errorf("模板草稿需要当前配置")
	}
	var spec Template
	var issues Errors
	sources := map[string]*yaml.Node{}
	readDocument(documentOverlay{name: "template-draft.yaml", data: data}, "template-draft.yaml", &spec, sources, &issues)
	if len(issues) > 0 {
		return spec, nil, issues
	}
	if !idPattern.MatchString(spec.ID) {
		return spec, nil, Errors{{File: "template-draft.yaml", Line: 1, Message: "模板 ID 必须由小写字母、数字或横线组成且不能为空"}}
	}
	name := "card-templates/" + spec.ID + ".yaml"
	copy := base.clone()
	copy.sources = maps.Clone(base.sources)
	if copy.sources == nil {
		copy.sources = map[string]*yaml.Node{}
	}
	copy.sources[name] = sources["template-draft.yaml"]
	copy.templateFiles = make([]string, len(copy.Templates))
	index := -1
	for i, item := range copy.Templates {
		copy.templateFiles[i] = "card-templates/" + item.ID + ".yaml"
		if item.ID == spec.ID {
			index = i
		}
	}
	if index >= 0 {
		copy.Templates[index] = spec
	} else {
		copy.Templates = append(copy.Templates, spec)
		copy.templateFiles = append(copy.templateFiles, name)
	}
	if err := Validate(copy); err != nil {
		return spec, nil, err
	}
	return spec, copy, nil
}

func validDocumentPath(name string) bool {
	return fs.ValidPath(name) && !strings.ContainsAny(name, "\\:") && name != "."
}
