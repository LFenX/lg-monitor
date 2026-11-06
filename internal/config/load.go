package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Issue struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
	cause   error
}

func (e Issue) Error() string {
	return fmt.Sprintf("%s 第 %d 行：%s", e.File, e.Line, e.Message)
}

func (e Issue) Unwrap() error { return e.cause }

type Errors []Issue

func (e Errors) Error() string {
	lines := make([]string, len(e))
	for i := range e {
		lines[i] = e[i].Error()
	}
	return strings.Join(lines, "\n")
}

func (e Errors) Unwrap() []error {
	result := make([]error, len(e))
	for i := range e {
		result[i] = e[i]
	}
	return result
}

func (e *Errors) add(file string, node *yaml.Node, message string, cause error) {
	line := 1
	if node != nil && node.Line > 0 {
		line = node.Line
	}
	*e = append(*e, Issue{File: file, Line: line, Message: message, cause: cause})
}

type Loader interface {
	Load() (*Config, error)
}

type FSLoader struct{ FS fs.FS }

func (l FSLoader) Load() (*Config, error) { return Load(l.FS) }

func Load(fsys fs.FS) (*Config, error) {
	if fsys == nil {
		return nil, fmt.Errorf("配置文件系统未设置")
	}
	cfg := &Config{sources: make(map[string]*yaml.Node)}
	var targets TargetsFile
	var rules RulesFile
	var notifiers NotifiersFile
	var issues Errors
	readDocument(fsys, "targets.yaml", &targets, cfg.sources, &issues)
	readDocument(fsys, "rules.yaml", &rules, cfg.sources, &issues)
	readDocument(fsys, "notifiers.yaml", &notifiers, cfg.sources, &issues)
	cfg.Targets, cfg.Rules = targets.Targets, rules.Rules
	cfg.FeishuApps, cfg.Notifiers = notifiers.FeishuApps, notifiers.Notifiers
	entries, err := fs.ReadDir(fsys, "card-templates")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		issues.add("card-templates", nil, "读取模板目录失败，请检查目录和访问权限", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		file := path.Join("card-templates", entry.Name())
		if strings.EqualFold(path.Ext(entry.Name()), ".yml") {
			issues.add(file, nil, "模板文件请使用 .yaml 扩展名："+file, nil)
			continue
		}
		if !strings.EqualFold(path.Ext(entry.Name()), ".yaml") {
			continue
		}
		var template Template
		readDocument(fsys, file, &template, cfg.sources, &issues)
		cfg.Templates = append(cfg.Templates, template)
		cfg.templateFiles = append(cfg.templateFiles, file)
	}
	if len(issues) > 0 {
		return nil, issues
	}
	if err := Validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

var yamlLine = regexp.MustCompile(`(?:line |第 )(\d+)`)

func readDocument(fsys fs.FS, file string, dst any, sources map[string]*yaml.Node, issues *Errors) {
	data, err := fs.ReadFile(fsys, file)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		issues.add(file, nil, "读取配置失败，请检查文件和访问权限", err)
		return
	}
	var doc yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	err = decoder.Decode(&doc)
	if errors.Is(err, io.EOF) {
		return
	}
	if err != nil {
		addYAMLError(issues, file, err)
		return
	}
	sources[file] = &doc
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			addYAMLError(issues, file, err)
		} else {
			issues.add(file, nodeAt(&extra), "一个配置文件只能包含一个 YAML 文档，请删除多余的 --- 分隔段", nil)
		}
		return
	}
	decoder = yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(dst); err != nil {
		addYAMLError(issues, file, err)
		return
	}
	checkScalarTypes(file, nodeAt(&doc), reflect.TypeOf(dst).Elem(), issues)
}

func addYAMLError(issues *Errors, file string, err error) {
	parts := []string{err.Error()}
	var typeError *yaml.TypeError
	if errors.As(err, &typeError) {
		parts = typeError.Errors
	}
	for _, part := range parts {
		line := 1
		if match := yamlLine.FindStringSubmatch(part); len(match) > 1 {
			line, _ = strconv.Atoi(match[1])
		}
		message := "YAML 语法或字段类型错误，请按配置 Schema 检查缩进和字段类型"
		if strings.Contains(part, "field ") && strings.Contains(part, "not found") {
			field := strings.Fields(strings.SplitN(part, "field ", 2)[1])[0]
			message = fmt.Sprintf("不支持字段 %s，请改正字段名或删除该字段", field)
		} else if strings.Contains(part, "already defined") {
			message = "字段重复定义，请合并或删除重复字段"
		}
		*issues = append(*issues, Issue{File: file, Line: line, Message: message, cause: err})
	}
}

func checkScalarTypes(file string, node *yaml.Node, typ reflect.Type, issues *Errors) {
	if node == nil || node.Tag == "!!null" {
		return
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if node.Kind == yaml.AliasNode {
		checkScalarTypes(file, node.Alias, typ, issues)
		return
	}
	if typ == reflect.TypeFor[Status]() {
		return
	}
	if typ == reflect.TypeFor[Duration]() && (node.Tag == "!!int" || node.Tag == "!!float") {
		issues.add(file, node, "时长缺少单位，请写成 10s 或 5m 这样的格式", nil)
		return
	}
	switch typ.Kind() {
	case reflect.String:
		if node.Tag != "!!str" {
			issues.add(file, node, "字段必须使用字符串，数字文本请加引号", nil)
		}
	case reflect.Int, reflect.Int64:
		if node.Tag != "!!int" {
			issues.add(file, node, "字段必须使用整数，请删除小数部分或引号", nil)
		}
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := strings.Split(field.Tag.Get("yaml"), ",")[0]
			if name != "" && name != "-" {
				checkScalarTypes(file, nodeAt(node, name), field.Type, issues)
			}
		}
	case reflect.Slice:
		for _, item := range node.Content {
			checkScalarTypes(file, item, typ.Elem(), issues)
		}
	case reflect.Map:
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Tag == "!!merge" {
				merged := nodeAt(node.Content[i+1])
				if merged.Kind == yaml.SequenceNode {
					for _, item := range merged.Content {
						checkScalarTypes(file, item, typ, issues)
					}
				} else {
					checkScalarTypes(file, merged, typ, issues)
				}
				continue
			}
			checkScalarTypes(file, node.Content[i], typ.Key(), issues)
			checkScalarTypes(file, node.Content[i+1], typ.Elem(), issues)
		}
	}
}

func nodeAt(node *yaml.Node, parts ...any) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	if node.Kind == yaml.AliasNode {
		return nodeAt(node.Alias, parts...)
	}
	if len(parts) == 0 {
		return node
	}
	switch key := parts[0].(type) {
	case int:
		if node.Kind == yaml.SequenceNode && key >= 0 && key < len(node.Content) {
			return nodeAt(node.Content[key], parts[1:]...)
		}
	case string:
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == key {
				return nodeAt(node.Content[i+1], parts[1:]...)
			}
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Tag == "!!merge" {
				merged := nodeAt(node.Content[i+1])
				if merged.Kind == yaml.SequenceNode {
					for _, item := range merged.Content {
						if found := nodeAt(item, parts...); found != nil {
							return found
						}
					}
				} else if found := nodeAt(merged, parts...); found != nil {
					return found
				}
			}
		}
	}
	return nil
}
