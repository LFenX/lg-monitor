package cards

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

var (
	mentionTag = regexp.MustCompile(`(?i)<\s*/?\s*at\b`)
	mentionID  = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

func (r *Renderer) execute(source string, data any) (string, error) {
	functions := template.FuncMap{
		"truncate": head,
		"default": func(fallback string, value any) string {
			if value == nil || fmt.Sprint(value) == "" {
				return fallback
			}
			return fmt.Sprint(value)
		},
		"duration": func(value any) (string, error) {
			switch v := value.(type) {
			case time.Duration:
				return chineseDuration(v), nil
			case int64:
				return chineseDuration(time.Duration(v) * time.Millisecond), nil
			case int:
				return chineseDuration(time.Duration(v) * time.Millisecond), nil
			case string:
				d, err := config.Duration(v).Parse()
				if err != nil {
					return "", fmt.Errorf("duration 需要 5m 等 Go 时长：%w", err)
				}
				return chineseDuration(d), nil
			default:
				return "", fmt.Errorf("duration 需要时长字符串或毫秒整数")
			}
		},
		"time": func(value any) (string, error) {
			switch v := value.(type) {
			case time.Time:
				return v.In(r.location).Format("2006-01-02 15:04:05"), nil
			case int64:
				return r.formatTime(v), nil
			case int:
				return r.formatTime(int64(v)), nil
			case string:
				if _, err := time.ParseInLocation("2006-01-02 15:04:05", v, r.location); err == nil {
					return v, nil
				}
				t, err := time.Parse(time.RFC3339, v)
				if err != nil {
					return "", fmt.Errorf("time 需要 RFC3339、本地格式时间或 Unix 毫秒：%w", err)
				}
				return t.In(r.location).Format("2006-01-02 15:04:05"), nil
			default:
				return "", fmt.Errorf("time 的参数类型无效")
			}
		},
		"join": strings.Join,
	}
	parsed, err := template.New("card").Option("missingkey=error").Funcs(functions).Parse(source)
	if err != nil {
		return "", fmt.Errorf("解析卡片模板失败：%w", err)
	}
	var out bytes.Buffer
	if err := parsed.Execute(&out, data); err != nil {
		return "", fmt.Errorf("执行卡片模板失败：%w", err)
	}
	return out.String(), nil
}

func transform(value any, f func(string) string) any {
	switch v := value.(type) {
	case string:
		return f(v)
	case []string:
		out := make([]string, len(v))
		for i := range v {
			out[i] = f(v[i])
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = transform(v[i], f)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = transform(item, f)
		}
		return out
	default:
		return value
	}
}

func (r *Renderer) raw(value any, data map[string]any, state string) (map[string]any, error) {
	if envelope, ok := value.(map[string]any); ok {
		if firing, exists := envelope["firing"]; exists {
			value = firing
			if state == "resolved" && envelope["resolved"] != nil {
				value = envelope["resolved"]
			}
		}
	}
	var rendered any
	if source, ok := value.(string); ok {
		// Variables in a JSON string template occur inside JSON string literals.
		escaped := transform(data, func(s string) string {
			encoded, _ := json.Marshal(escapeMarkdown(s))
			return string(encoded[1 : len(encoded)-1])
		})
		text, err := r.execute(source, escaped)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(text), &rendered); err != nil {
			return nil, fmt.Errorf("raw 渲染结果不是合法 JSON：%w", err)
		}
	} else {
		var err error
		rendered, err = r.rawValue(value, transform(data, escapeMarkdown))
		if err != nil {
			return nil, err
		}
	}
	result, ok := rendered.(map[string]any)
	if !ok || result["schema"] != "2.0" {
		return nil, fmt.Errorf("raw 必须是 schema 为 2.0 的卡片对象")
	}
	body, ok := result["body"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("raw 卡片缺少 body 对象")
	}
	if _, ok := body["elements"].([]any); !ok {
		return nil, fmt.Errorf("raw 卡片的 body.elements 必须为数组")
	}
	return result, nil
}

func (r *Renderer) rawValue(value, data any) (any, error) {
	switch v := value.(type) {
	case string:
		return r.execute(v, data)
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			rendered, err := r.rawValue(item, data)
			if err != nil {
				return nil, err
			}
			out[key] = rendered
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			rendered, err := r.rawValue(item, data)
			if err != nil {
				return nil, err
			}
			out[i] = rendered
		}
		return out, nil
	default:
		return value, nil
	}
}

func mentions(spec config.Template, in Input) (string, error) {
	key := in.Alert.Severity
	if in.Alert.State == "resolved" {
		key = "resolved"
	}
	value := spec.Mention[key]
	if value == nil {
		return "", nil
	}
	if value == "all" {
		return "<at id=all></at>", nil
	}
	var ids []string
	switch list := value.(type) {
	case []any:
		for _, item := range list {
			id, ok := item.(string)
			if !ok {
				return "", fmt.Errorf("mention 只能为 all 或用户标识列表")
			}
			ids = append(ids, id)
		}
	case []string:
		ids = list
	default:
		return "", fmt.Errorf("mention 只能为 all 或用户标识列表")
	}
	var out []string
	for _, id := range ids {
		if !mentionID.MatchString(id) || id == "all" {
			return "", fmt.Errorf("mention 包含无效用户标识")
		}
		out = append(out, "<at id="+id+"></at>")
	}
	return strings.Join(out, " "), nil
}

func validURL(text string) bool {
	u, err := url.Parse(text)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && !strings.ContainsAny(text, "\r\n\t ")
}
