package cards

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/config"
)

func TestCallbackActionsSurviveAllRenderPathsAndRemainFiringOnly(t *testing.T) {
	r, _, _ := testRenderer(t, config.Template{ID: "custom-actions", Extends: "detailed", Title: "自定义动作"}, config.Template{ID: "raw-actions", Raw: map[string]any{"schema": "2.0", "body": map[string]any{"elements": []any{map[string]any{"tag": "markdown", "content": "{{.Alert.Summary}}"}}}}})
	in := Sample("firing")
	value := json.RawMessage(`{"lgmonVersion":1,"alertId":"fake-alert","eventKey":"firing:fake","operation":"ack","signature":"abcdef"}`)
	in.Actions = []CallbackAction{{Text: "确认", Value: value}, {Text: "静音 1 小时", Value: json.RawMessage(strings.Replace(string(value), `"ack"`, `"silence_1h"`, 1))}}
	for _, id := range []string{"standard", "compact", "detailed", "digest", "custom-actions", "raw-actions", "unknown-actions"} {
		t.Run(id, func(t *testing.T) {
			result := r.Render(id, in)
			decode(t, result)
			if bytes.Count(result.JSON, []byte(`"type":"callback"`)) != 2 || !bytes.Contains(result.JSON, value) {
				t.Fatal("actions missing or changed", string(result.JSON))
			}
			frozen, err := r.Freeze(id)
			if err == nil {
				if next := frozen.RenderAt(in, r.now()); !bytes.Contains(next.JSON, value) {
					t.Fatal("frozen update lost signature")
				}
			}
		})
	}
	in.Detail = strings.Repeat("很长的诊断文本", 20000)
	if result := r.Render("detailed", in); len(result.JSON) > MaxBytes || !bytes.Contains(result.JSON, value) {
		t.Fatal("clipping lost actions", len(result.JSON))
	}
	if result := r.StandardFallback("custom-actions", in); !bytes.Contains(result.JSON, value) {
		t.Fatal("platform fallback lost actions")
	}
	in.Alert.State = "resolved"
	if result := r.Render("standard", in); bytes.Contains(result.JSON, []byte(`"type":"callback"`)) {
		t.Fatal("recovery card became executable")
	}
}
