package cards

import (
	"embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"gopkg.in/yaml.v3"
)

const MaxBytes = 28_000

//go:embed presets/*.yaml
var presetFiles embed.FS

type TemplateSource interface {
	Snapshot() *config.Config
}

type Options struct {
	Now      func() time.Time
	Location *time.Location
	Logger   *slog.Logger
}

type Renderer struct {
	source   TemplateSource
	presets  map[string]config.Template
	now      func() time.Time
	location *time.Location
	logger   *slog.Logger
}

type Result struct {
	JSON      json.RawMessage `json:"card"`
	Template  string          `json:"template"`
	Fallback  bool            `json:"fallback"`
	Truncated bool            `json:"truncated"`
	Warning   string          `json:"warning,omitempty"`
}

type Entry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Preset bool   `json:"preset"`
}

func New(source TemplateSource, opts Options) (*Renderer, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	r := &Renderer{source: source, presets: make(map[string]config.Template), now: opts.Now, location: opts.Location, logger: opts.Logger}
	for _, id := range []string{"standard", "compact", "detailed", "digest"} {
		data, err := presetFiles.ReadFile("presets/" + id + ".yaml")
		if err != nil {
			return nil, fmt.Errorf("读取内置卡片模板失败：%w", err)
		}
		var spec config.Template
		if err := yaml.Unmarshal(data, &spec); err != nil {
			return nil, fmt.Errorf("解析内置卡片模板失败：%w", err)
		}
		r.presets[id] = spec
	}
	return r, nil
}

func (r *Renderer) List() []Entry {
	entries := make([]Entry, 0, len(r.presets))
	for id, spec := range r.presets {
		entries = append(entries, Entry{ID: id, Name: spec.Name, Preset: true})
	}
	if r.source != nil {
		if snapshot := r.source.Snapshot(); snapshot != nil {
			for _, spec := range snapshot.Templates {
				entries = append(entries, Entry{ID: spec.ID, Name: spec.Name})
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Preset != entries[j].Preset {
			return entries[i].Preset
		}
		return entries[i].ID < entries[j].ID
	})
	return entries
}

func (r *Renderer) definition(id string) (config.Template, error) {
	if spec, ok := r.presets[id]; ok {
		return spec, nil
	}
	if r.source != nil {
		if snapshot := r.source.Snapshot(); snapshot != nil {
			for _, custom := range snapshot.Templates {
				if custom.ID != id {
					continue
				}
				if custom.Raw != nil {
					return custom, nil
				}
				base, ok := r.presets[custom.Extends]
				if !ok {
					return config.Template{}, fmt.Errorf("模板 %s 的 extends 无效", id)
				}
				base.ID, base.Name = id, custom.Name
				if custom.Title != "" {
					base.Title = custom.Title
				}
				if custom.Colors != nil {
					colors := make(map[string]string, len(base.Colors)+len(custom.Colors))
					for key, value := range base.Colors {
						colors[key] = value
					}
					for key, value := range custom.Colors {
						colors[key] = value
					}
					base.Colors = colors
				}
				if custom.Fields != nil {
					base.Fields = custom.Fields
				}
				base.ExtraFields, base.Mention = custom.ExtraFields, custom.Mention
				base.Buttons, base.Note = custom.Buttons, custom.Note
				return base, nil
			}
		}
	}
	return config.Template{}, fmt.Errorf("模板 %s 不存在", id)
}

func (r *Renderer) Definition(id string) (config.Template, error) {
	if r.source != nil {
		if cfg := r.source.Snapshot(); cfg != nil {
			for _, spec := range cfg.Templates {
				if spec.ID == id {
					return spec, nil
				}
			}
		}
	}
	spec, err := r.definition(id)
	if err != nil {
		return config.Template{}, err
	}
	// HTTP callers receive their own definition rather than mutable preset maps.
	data, err := json.Marshal(spec)
	if err != nil {
		return config.Template{}, fmt.Errorf("复制模板定义失败：%w", err)
	}
	var copy config.Template
	if err := json.Unmarshal(data, &copy); err != nil {
		return copy, fmt.Errorf("读取模板定义副本失败：%w", err)
	}
	return copy, nil
}

// Check reports template errors instead of hiding them behind the delivery fallback.
func (r *Renderer) Check(id string) error {
	spec, err := r.definition(first(id, "standard"))
	if err != nil {
		return err
	}
	now := r.now()
	for _, state := range []string{"firing", "resolved", "digest"} {
		sample := Sample(state)
		sample.Events = []collector.EventLogRecord{{Channel: "Application", Provider: "Application Error", EventID: 1000, RecordID: 1, TS: sample.Alert.StartedAt, Subject: "fake.exe", Message: "虚构诊断事件"}}
		if _, err := r.limited(spec, sample, now, ""); err != nil {
			return fmt.Errorf("模板 %s 的 %s 样例校验失败：%w", id, state, err)
		}
	}
	return nil
}

func (r *Renderer) Render(id string, in Input) Result {
	id = first(id, "standard")
	if in.DashboardURL == "" && r.source != nil {
		if cfg := r.source.Snapshot(); cfg != nil {
			in.DashboardURL = cfg.Settings.PublicBaseURL
		}
	}
	now := r.now()
	spec, err := r.definition(id)
	if err == nil {
		var result Result
		result, err = r.limited(spec, in, now, "")
		if err == nil {
			return result
		}
	}
	return r.standardFallback(id, in, now)
}

func (r *Renderer) StandardFallback(id string, in Input) Result {
	if in.DashboardURL == "" && r.source != nil {
		if cfg := r.source.Snapshot(); cfg != nil {
			in.DashboardURL = cfg.Settings.PublicBaseURL
		}
	}
	return r.standardFallback(id, in, r.now())
}

func (r *Renderer) standardFallback(id string, in Input, now time.Time) Result {
	// Template execution errors can contain untrusted data; only the identifier is logged.
	r.logger.Error("Custom card template failed; using standard", "template", head(id, 80))
	warning := "自定义模板 " + head(id, 80) + " 出错，已使用标准模板"
	result, fallbackErr := r.limited(r.presets["standard"], bounded(in), now, warning)
	if fallbackErr != nil {
		// This literal remains sendable even if an embedded template is broken.
		value := card([]any{markdown(escapeMarkdown(in.Alert.Summary)), markdown(escapeMarkdown(warning))})
		value["body"].(map[string]any)["elements"].([]any)[0] = markdown(escapeMarkdown(head(in.Alert.Summary, 400)))
		data, _ := json.Marshal(value)
		result = Result{JSON: data, Template: "standard", Warning: warning}
	}
	result.Fallback, result.Warning = true, warning
	return result
}

func (r *Renderer) limited(spec config.Template, in Input, now time.Time, footer string) (Result, error) {
	// Normalize once so later size-limit passes can actually remove the generated tail.
	in = r.diagnosticInput(in)
	for pass, limits := range []struct{ log, alerts int }{{-1, 20}, {2048, 20}, {512, 10}, {0, 1}} {
		current := in
		if limits.log >= 0 {
			current.LogTail = tail(in.LogTail, limits.log)
		}
		value, err := r.build(spec, current, now, limits.alerts)
		if err != nil {
			return Result{}, err
		}
		if footer != "" {
			appendElement(value, markdown(escapeMarkdown(footer)))
		}
		data, err := json.Marshal(value)
		if err != nil {
			return Result{}, fmt.Errorf("卡片 JSON 编码失败：%w", err)
		}
		if len(data) <= MaxBytes {
			return Result{JSON: data, Template: spec.ID, Truncated: pass > 0 || len(in.Alerts) > limits.alerts}, nil
		}
	}
	value, err := r.build(r.presets["compact"], bounded(in), now, 0)
	if err != nil {
		return Result{}, err
	}
	warning := "内容超过卡片大小限制，已使用简洁模板"
	appendElement(value, markdown(warning))
	if footer != "" {
		appendElement(value, markdown(escapeMarkdown(footer)))
	}
	data, err := json.Marshal(value)
	if err != nil {
		return Result{}, fmt.Errorf("简洁卡片 JSON 编码失败：%w", err)
	}
	if len(data) > MaxBytes {
		return Result{}, fmt.Errorf("简洁卡片仍超过 %d 字节", MaxBytes)
	}
	return Result{JSON: data, Template: "compact", Fallback: true, Truncated: true, Warning: warning}, nil
}

func bounded(in Input) Input {
	in.Alert.Summary = head(in.Alert.Summary, 400)
	in.Rule.Name, in.Rule.ID = head(in.Rule.Name, 100), head(in.Rule.ID, 100)
	in.Target.Name, in.Target.ID = head(in.Target.Name, 100), head(in.Target.ID, 100)
	in.Host.Name, in.Host.CollectorID = head(in.Host.Name, 100), head(in.Host.CollectorID, 100)
	in.Alert.CollectorID = head(in.Alert.CollectorID, 100)
	in.Detail, in.Value = head(in.Detail, 1000), head(in.Value, 200)
	if len(in.DashboardURL) > 2048 || !validURL(in.DashboardURL) {
		in.DashboardURL = ""
	}
	in.Processes, in.LogTail, in.Diagnosis, in.Alerts = nil, "", "", nil
	in.Events = nil
	return in
}

func card(elements []any) map[string]any {
	return map[string]any{"schema": "2.0", "body": map[string]any{"elements": elements}}
}

func markdown(content string) map[string]any {
	return map[string]any{"tag": "markdown", "content": content}
}

func appendElement(value map[string]any, element any) {
	body := value["body"].(map[string]any)
	body["elements"] = append(body["elements"].([]any), element)
}

var markdownReplacer = strings.NewReplacer(
	"\\", "\\\\", "&", "&amp;", "<", "&lt;", ">", "&gt;", "*", "\\*", "_", "\\_",
	"[", "\\[", "]", "\\]", "`", "\\`", "~", "\\~", "#", "\\#", "|", "\\|", "!", "\\!",
)

func escapeMarkdown(s string) string { return markdownReplacer.Replace(s) }
