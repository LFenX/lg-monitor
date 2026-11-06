package config

import (
	"fmt"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"
)

// Definitions disappear from the root AST, including a definition with the root's name.
func checkTemplateActions(source string) error {
	for offset := 0; offset < len(source); {
		start := strings.Index(source[offset:], "{{")
		if start < 0 {
			return nil
		}
		offset += start + 2
		if offset < len(source) && source[offset] == '-' {
			offset++
		}
		for offset < len(source) && strings.ContainsRune(" \t\r\n", rune(source[offset])) {
			offset++
		}
		if strings.HasPrefix(source[offset:], "/*") {
			end := strings.Index(source[offset+2:], "*/")
			if end < 0 {
				return nil
			}
			offset += end + 4
			end = strings.Index(source[offset:], "}}")
			if end < 0 {
				return nil
			}
			offset += end + 2
			continue
		}
		end := offset
		for end < len(source) && ((source[end] >= 'a' && source[end] <= 'z') || (source[end] >= 'A' && source[end] <= 'Z')) {
			end++
		}
		switch source[offset:end] {
		case "define", "template", "block":
			return fmt.Errorf("卡片模板不允许 define、template 或 block")
		}
		var quote byte
		for offset < len(source) {
			current := source[offset]
			if quote != 0 {
				if current == quote {
					quote = 0
				} else if current == '\\' && quote != '`' {
					offset++
				}
			} else {
				if strings.HasPrefix(source[offset:], "}}") {
					offset += 2
					break
				}
				if current == '"' || current == '\'' || current == '`' {
					quote = current
				}
			}
			offset++
		}
	}
	return nil
}

func allowedRange(node parse.Node) bool {
	var path []string
	switch value := node.(type) {
	case *parse.PipeNode:
		return len(value.Cmds) == 1 && len(value.Cmds[0].Args) == 1 && allowedRange(value.Cmds[0].Args[0])
	case *parse.FieldNode:
		path = value.Ident
	case *parse.VariableNode:
		if len(value.Ident) < 2 || value.Ident[0] != "$" {
			return false
		}
		path = value.Ident[1:]
	default:
		return false
	}
	name := strings.Join(path, ".")
	return name == "Alerts" || name == "Processes" || name == "Target.Tags"
}

func checkTemplateNode(node parse.Node, inRange bool) error {
	if node == nil {
		return nil
	}
	switch value := node.(type) {
	case *parse.ListNode:
		if value == nil {
			return nil
		}
		for _, child := range value.Nodes {
			if err := checkTemplateNode(child, inRange); err != nil {
				return err
			}
		}
	case *parse.RangeNode:
		if inRange {
			return fmt.Errorf("卡片模板不允许嵌套 range")
		}
		if !allowedRange(value.Pipe) {
			return fmt.Errorf("range 只能遍历 .Alerts、.Processes、.Target.Tags 或对应的 $ 根路径，不能遍历数字、变量或函数结果")
		}
		if err := checkTemplateNode(value.List, true); err != nil {
			return err
		}
		return checkTemplateNode(value.ElseList, true)
	case *parse.IfNode:
		if err := checkTemplateNode(value.List, inRange); err != nil {
			return err
		}
		return checkTemplateNode(value.ElseList, inRange)
	case *parse.WithNode:
		if err := checkTemplateNode(value.List, inRange); err != nil {
			return err
		}
		return checkTemplateNode(value.ElseList, inRange)
	case *parse.TemplateNode:
		return fmt.Errorf("卡片模板不允许调用其他模板")
	}
	return nil
}

func ParseCardText(source string, functions template.FuncMap) (*template.Template, error) {
	if err := checkTemplateActions(source); err != nil {
		return nil, err
	}
	parsed, err := template.New("card").Option("missingkey=error").Funcs(functions).Parse(source)
	if err != nil {
		return nil, fmt.Errorf("解析卡片模板失败：%w", err)
	}
	if len(parsed.Templates()) != 1 {
		return nil, fmt.Errorf("卡片模板不允许定义其他模板")
	}
	if err := checkTemplateNode(parsed.Tree.Root, false); err != nil {
		return nil, err
	}
	return parsed, nil
}

type templateText struct {
	path  []any
	value string
}

func templateTexts(spec Template) []templateText {
	texts := []templateText{{[]any{"title"}, spec.Title}, {[]any{"note"}, spec.Note}}
	for i, field := range spec.ExtraFields {
		texts = append(texts, templateText{[]any{"extraFields", i, "label"}, field.Label}, templateText{[]any{"extraFields", i, "value"}, field.Value})
	}
	for i, button := range spec.Buttons {
		texts = append(texts, templateText{[]any{"buttons", i, "text"}, button.Text}, templateText{[]any{"buttons", i, "url"}, button.URL})
	}
	var walk func(any, []any)
	walk = func(value any, path []any) {
		switch value := value.(type) {
		case string:
			texts = append(texts, templateText{append([]any(nil), path...), value})
		case []any:
			for i, item := range value {
				walk(item, append(append([]any(nil), path...), i))
			}
		case map[string]any:
			keys := make([]string, 0, len(value))
			for key := range value {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(value[key], append(append([]any(nil), path...), key))
			}
		}
	}
	walk(spec.Raw, []any{"raw"})
	return texts
}

func validateTemplateText(source string) error {
	stub := func(...any) string { return "" }
	_, err := ParseCardText(source, template.FuncMap{"truncate": stub, "default": stub, "duration": stub, "time": stub, "join": stub})
	return err
}
