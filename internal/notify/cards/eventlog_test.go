package cards

import (
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
)

func TestEventLogTemplateLimitsUnicodeAndFreezesOnlyEventsForUpdate(t *testing.T) {
	r, source, _ := testRenderer(t, config.Template{ID: "frozen", Extends: "detailed", Title: "原始标题"})
	frozen, err := r.Freeze("frozen")
	if err != nil {
		t.Fatal(err)
	}
	if !frozen.UsesEvents() {
		t.Fatal("detailed omitted event collection")
	}
	input := Sample("firing")
	for i := range 7 {
		input.Events = append(input.Events, collector.EventLogRecord{TS: 1000 + int64(i)*1000, Provider: "Fake", EventID: 1000, Message: strings.Repeat("测", 201)})
	}
	at := time.Date(2026, 1, 2, 3, 9, 5, 0, time.UTC)
	data := r.data(input, at, 0)["Events"].([]any)
	if len(data) != 5 || data[0].(map[string]any)["Time"] != "1970-01-01 08:00:03" || len([]rune(data[0].(map[string]any)["Message"].(string))) != 200 {
		t.Fatal("event order, clock formatting, or Unicode limit differs")
	}
	source.cfg.Templates[0].Title = "修改后的标题"
	first := frozen.RenderAt(input, at)
	if first.Fallback || !UpdateEnabled(first.JSON) || !strings.Contains(string(first.JSON), "原始标题") || strings.Contains(string(first.JSON), "修改后的标题") {
		t.Fatalf("template snapshot changed: %s", first.JSON)
	}
	plain, _ := r.Freeze("standard")
	if plain.UsesEvents() {
		t.Fatal("standard template waits for unused events")
	}
	for _, tc := range []struct {
		source string
		uses   bool
	}{{`{{range .Events}}{{.Message}}{{end}}`, true}, {`{{range $.Events}}{{.Time}}{{end}}`, true}, {`{{default ".Events" .Value}}`, false}} {
		spec := config.Template{Note: tc.source}
		if config.TemplateUsesField(spec, "Events") != tc.uses {
			t.Fatalf("incorrect field reference detection: %s", tc.source)
		}
	}
}

func TestEventLogTemplateValidationExecutesEventItemFields(t *testing.T) {
	r, _, _ := testRenderer(t, config.Template{ID: "invalid-event", Extends: "standard", Note: `{{range .Events}}{{.Missing}}{{end}}`})
	if err := r.Check("invalid-event"); err == nil {
		t.Fatal("empty preview hid an invalid event item field")
	}
}
