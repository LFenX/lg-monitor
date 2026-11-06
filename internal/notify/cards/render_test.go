package cards

import (
	"bytes"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

var updateGoldens = flag.Bool("update-card-goldens", false, "rewrite card fixtures for review")

type snapshotSource struct{ cfg *config.Config }

func (s *snapshotSource) Snapshot() *config.Config { return s.cfg }

func testRenderer(t *testing.T, specs ...config.Template) (*Renderer, *snapshotSource, *bytes.Buffer) {
	t.Helper()
	source := &snapshotSource{cfg: &config.Config{Templates: specs}}
	var logs bytes.Buffer
	r, err := New(source, Options{
		Now:      func() time.Time { return time.Date(2026, 1, 2, 3, 9, 5, 0, time.UTC) },
		Location: time.FixedZone("test-local", 8*60*60),
		Logger:   slog.New(slog.NewTextHandler(&logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, source, &logs
}

func decode(t *testing.T, result Result) map[string]any {
	t.Helper()
	if !json.Valid(result.JSON) || len(result.JSON) > MaxBytes {
		t.Fatalf("invalid card: bytes=%d", len(result.JSON))
	}
	var value map[string]any
	if err := json.Unmarshal(result.JSON, &value); err != nil {
		t.Fatal(err)
	}
	if value["schema"] != "2.0" {
		t.Fatal("schema must be 2.0")
	}
	return value
}

func allText(value any) string {
	var out []string
	transform(value, func(s string) string { out = append(out, s); return s })
	return strings.Join(out, "\n")
}

func elements(value map[string]any) []any {
	return value["body"].(map[string]any)["elements"].([]any)
}

func TestPresetGoldens(t *testing.T) {
	r, _, _ := testRenderer(t)
	for _, id := range []string{"standard", "compact", "detailed", "digest"} {
		for _, state := range []string{"firing", "resolved", "digest"} {
			t.Run(id+"/"+state, func(t *testing.T) {
				result := r.Render(id, Sample(state))
				decode(t, result)
				if result.Fallback || result.Truncated {
					t.Fatalf("unexpected fallback: %+v", result)
				}
				path := filepath.Join("testdata", id+"-"+state+".golden.json")
				if *updateGoldens {
					if err := os.MkdirAll("testdata", 0o700); err != nil {
						t.Fatal(err)
					}
					var pretty bytes.Buffer
					if err := json.Indent(&pretty, result.JSON, "", "  "); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, append(pretty.Bytes(), '\n'), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				golden, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var compact bytes.Buffer
				if err := json.Compact(&compact, golden); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(compact.Bytes(), result.JSON) {
					t.Fatalf("card differs from %s", path)
				}
			})
		}
	}
}

func TestPresetSemantics(t *testing.T) {
	r, _, _ := testRenderer(t)
	for severity, color := range map[string]string{"critical": "carmine", "warning": "orange", "info": "blue"} {
		in := Sample("firing")
		in.Alert.Severity = severity
		value := decode(t, r.Render("standard", in))
		if got := value["header"].(map[string]any)["template"]; got != color {
			t.Fatalf("severity %s: %v", severity, got)
		}
	}
	standard := decode(t, r.Render("standard", Sample("resolved")))
	if standard["header"].(map[string]any)["template"] != "green" {
		t.Fatal("recovery header must be green")
	}
	text := allText(standard)
	for _, want := range []string{"主机", "目标", "开始时间", "持续了多久", "当前值", "5分", "2026-01-02 11:04:05", "打开面板"} {
		if !strings.Contains(text, want) {
			t.Errorf("standard missing %q", want)
		}
	}
	for _, item := range elements(standard) {
		v := item.(map[string]any)
		if v["tag"] == "column_set" && len(v["columns"].([]any)) > 2 {
			t.Fatal("fields must have at most two columns")
		}
	}
	in := Sample("firing")
	in.DashboardURL = ""
	if strings.Contains(allText(decode(t, r.Render("standard", in))), "打开面板") {
		t.Fatal("empty dashboard URL must hide the button")
	}
	compact := decode(t, r.Render("compact", Sample("resolved")))
	if compact["header"] != nil || len(elements(compact)) != 2 || !strings.Contains(allText(compact), "持续了 5分") {
		t.Fatal("compact recovery must have two lines, duration, and no header")
	}
	detail := decode(t, r.Render("detailed", Sample("firing")))
	found := false
	for _, item := range elements(detail) {
		v := item.(map[string]any)
		if v["tag"] == "collapsible_panel" {
			found = true
			if v["expanded"] != false {
				t.Fatal("log panel must start collapsed")
			}
		}
	}
	if !found {
		t.Fatal("missing collapsible log panel")
	}
	for _, want := range []string{"PID 1234", "CPU 1.25%", "24.0 MiB", "原因与建议", "复制给 Agent 排查"} {
		if !strings.Contains(allText(detail), want) {
			t.Errorf("detailed missing %q", want)
		}
	}
	list := Sample("digest")
	list.Alerts = nil
	for i := 0; i < 25; i++ {
		list.Alerts = append(list.Alerts, Sample("firing"))
	}
	digest := decode(t, r.Render("digest", list))
	if len(elements(digest)) != 21 || !strings.Contains(allText(digest), "还有 5 条") {
		t.Fatal("digest must show twenty entries and the overflow count")
	}
}

func TestEscapingAndOnlyConfiguredMentions(t *testing.T) {
	r, _, _ := testRenderer(t, config.Template{ID: "oncall", Extends: "standard", Mention: map[string]any{"critical": "all"}})
	in := Sample("firing")
	in.Alert.Summary = "引号\"\n**加粗** _下划线_ [链接](x) `代码` \\ <at id=all></at>"
	for _, id := range []string{"standard", "compact", "detailed"} {
		value := decode(t, r.Render(id, in))
		text := allText(value)
		for _, want := range []string{"引号\"\n", `\*\*加粗\*\*`, `\_下划线\_`, `\[链接\]`, `\` + "`代码" + `\` + "`", "&lt;at id=all&gt;"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing escaped value %q", id, want)
			}
		}
		if mentionTag.MatchString(text) {
			t.Fatal("untrusted mention survived")
		}
	}
	value := decode(t, r.Render("oncall", in))
	if strings.Count(allText(value), "<at id=all></at>") != 1 {
		t.Fatal("configured mention must appear exactly once")
	}
	in.Alert.State = "resolved"
	if mentionTag.MatchString(allText(decode(t, r.Render("oncall", in)))) {
		t.Fatal("critical mention must not leak into recovery")
	}
}

func TestRawObjectStringAndStateFallback(t *testing.T) {
	body := func(content string) map[string]any {
		return card([]any{markdown(content)})
	}
	r, _, _ := testRenderer(t,
		config.Template{ID: "object", Raw: map[string]any{"firing": body(`{{if eq .Alert.State "resolved"}}恢复{{else}}触发{{end}}：{{.Alert.Summary}} <At id=all></At>`)}},
		config.Template{ID: "string", Raw: `{"schema":"2.0","body":{"elements":[{"tag":"markdown","content":"{{.Alert.Summary}}"}]}}`},
		config.Template{ID: "states", Raw: map[string]any{"firing": body("故障"), "resolved": body("已恢复")}},
	)
	for _, id := range []string{"object", "string"} {
		for _, state := range []string{"firing", "resolved"} {
			in := Sample(state)
			in.Alert.Summary = "演示\"\n*内容* <at id=all></at>"
			result := r.Render(id, in)
			text := allText(decode(t, result))
			if result.Fallback || mentionTag.MatchString(text) || !strings.Contains(text, "演示\"\n\\*内容\\*") {
				t.Fatalf("%s %s failed: %s", id, state, text)
			}
			if id == "object" && state == "resolved" && !strings.Contains(text, "恢复：") {
				t.Fatal("missing resolved fallback to firing template")
			}
		}
	}
	if !strings.Contains(allText(decode(t, r.Render("states", Sample("resolved")))), "已恢复") {
		t.Fatal("resolved raw variant was not selected")
	}
}

func TestTemplateFailureFallbackAndStrictValidation(t *testing.T) {
	for name, spec := range map[string]config.Template{
		"missing": {ID: "bad", Extends: "standard", Title: "{{.Alert.Missing}}"},
		"syntax":  {ID: "bad", Extends: "standard", Title: "{{"},
		"field":   {ID: "bad", Extends: "standard", Fields: []string{"unknown"}},
		"color":   {ID: "bad", Extends: "standard", Colors: map[string]string{"critical": "unknown"}},
		"json":    {ID: "bad", Raw: "{"},
		"schema":  {ID: "bad", Raw: map[string]any{"schema": "1.0"}},
		"mention": {ID: "bad", Extends: "standard", Mention: map[string]any{"critical": []any{"x> <at id=all>"}}},
	} {
		t.Run(name, func(t *testing.T) {
			r, _, logs := testRenderer(t, spec)
			if err := r.Check("bad"); err == nil {
				t.Fatal("strict validation accepted broken template")
			}
			in := Sample("firing")
			in.Alert.Summary = "private-test-content"
			result := r.Render("bad", in)
			value := decode(t, result)
			if !result.Fallback || result.Template != "standard" || !strings.Contains(allText(value), "自定义模板 bad 出错，已使用标准模板") {
				t.Fatalf("missing standard fallback: %+v", result)
			}
			if !strings.Contains(logs.String(), "using standard") || strings.Contains(logs.String(), in.Alert.Summary) {
				t.Fatal("missing or unsafe fallback log")
			}
		})
	}
}

func TestSizeReduction(t *testing.T) {
	r, _, _ := testRenderer(t)
	in := Sample("firing")
	in.LogTail = strings.Repeat("日志***<at id=all>\n", 5000) + "最后一行"
	result := r.Render("detailed", in)
	value := decode(t, result)
	if !result.Truncated || result.Template != "detailed" || !strings.Contains(allText(value), "最后一行") {
		t.Fatal("log tail must be shortened before changing layout")
	}
	list := Sample("digest")
	list.Alerts = make([]Input, 20)
	for i := range list.Alerts {
		list.Alerts[i] = Sample("firing")
		list.Alerts[i].Target.Name = strings.Repeat("长名称", 1000)
	}
	result = r.Render("digest", list)
	value = decode(t, result)
	if !result.Truncated || result.Template != "digest" || !strings.Contains(allText(value), "还有 19 条") {
		t.Fatal("alert list must be shortened before changing layout")
	}
	in.Alert.Summary = strings.Repeat("长告警*", 20_000)
	result = r.Render("standard", in)
	decode(t, result)
	if !result.Truncated || !result.Fallback || result.Template != "compact" {
		t.Fatalf("oversized card must fall back to compact: %+v", result)
	}
}

func TestHotReloadAndInheritance(t *testing.T) {
	r, source, _ := testRenderer(t, config.Template{ID: "custom", Extends: "standard", Title: "第一版", Fields: []string{"target", "host"}, ExtraFields: []config.TemplateField{{Label: "附加", Value: "{{.Target.ID}}"}}})
	first := decode(t, r.Render("custom", Sample("firing")))
	if first["header"].(map[string]any)["title"].(map[string]any)["content"] != "第一版" || !strings.Contains(allText(first), "附加") {
		t.Fatal("custom overrides were not rendered")
	}
	columns := elements(first)[0].(map[string]any)["columns"].([]any)
	if !strings.Contains(allText(columns[0]), "目标") || !strings.Contains(allText(columns[1]), "主机") {
		t.Fatal("custom field order was not preserved")
	}
	source.cfg = &config.Config{Templates: []config.Template{{ID: "custom", Extends: "compact", Title: "第二版"}}}
	second := decode(t, r.Render("custom", Sample("firing")))
	if second["header"] != nil || !strings.Contains(allText(second), "第二版") {
		t.Fatal("render must use the latest complete snapshot")
	}
	if err := r.Check("standard"); err != nil {
		t.Fatal(err)
	}
}

func TestExamplesAndHelpers(t *testing.T) {
	files, err := os.ReadDir(filepath.Join("..", "..", "..", "examples", "card-templates"))
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "card-templates", file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		fsys["card-templates/"+file.Name()] = &fstest.MapFile{Data: data}
	}
	cfg, err := config.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	r, _, _ := testRenderer(t, cfg.Templates...)
	for _, entry := range r.List() {
		if err := r.Check(entry.ID); err != nil {
			t.Fatal(err)
		}
	}
	out, err := r.execute(`{{duration "65s"}}|{{time 1767323045000}}|{{truncate "中文测试" 2}}|{{default "缺省" .Value}}`, map[string]any{"Value": ""})
	if err != nil || out != "1分5秒|2026-01-02 11:04:05|中文…|缺省" {
		t.Fatalf("helpers: %q, %v", out, err)
	}
	if !reflect.DeepEqual(cfg.Templates[0].Mention, map[string]any{"critical": "all"}) {
		t.Fatal("render modified source configuration")
	}
}
