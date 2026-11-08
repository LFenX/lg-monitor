package cards

import (
	"net/url"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
)

func TestDetailedDiagnosticLinkAndMetadataTailKeepExistingEvents(t *testing.T) {
	r, _, _ := testRenderer(t)
	in := Sample("firing")
	in.LogTail = ""
	in.Alert.ID = "fake /?#告警"
	in.DashboardURL = "https://example.test/base/#/old"
	in.Events = []collector.EventLogRecord{{TS: 1000, Provider: "Application Error", EventID: 1000, Subject: "fake.exe", Message: "existing-event-detail"}}
	result := r.Render("detailed", in)
	text := allText(decode(t, result))
	if result.Fallback || !strings.Contains(text, "https://example.test/base/#/alerts/"+url.PathEscape(in.Alert.ID)+"/diagnostics") || !strings.Contains(text, "查看诊断包") || !strings.Contains(text, "get_alert_diagnostics") || !strings.Contains(text, "正文未导出") || !strings.Contains(text, "existing-event-detail") {
		t.Fatal("diagnostic link, safe tail, or existing event details missing", text)
	}
	in.DashboardURL = ""
	text = allText(decode(t, r.Render("detailed", in)))
	if strings.Contains(text, "查看诊断包") || !strings.Contains(text, "lgmon diagnose") {
		t.Fatal("unconfigured public address produced a link or lost local handoff")
	}
	in.Events[0].Provider = strings.Repeat("测", 4000)
	in.Events[0].Subject = strings.Repeat("x", 3000)
	// Normalization must happen before the truncation passes, not regenerate the full tail.
	spec, err := r.definition("detailed")
	if err != nil {
		t.Fatal(err)
	}
	in.Events[0].Message = ""
	in.Events = append(in.Events, in.Events[0])
	result = r.Render("detailed", in)
	decode(t, result)
	spec.Extends = "standard"
	spec.Note = "{{.LogTail}}"
	bounded, err := r.limited(spec, in, r.now(), "")
	if err != nil || bounded.Fallback || !bounded.Truncated || len(bounded.JSON) > MaxBytes {
		t.Fatal("generated log tail bypassed card size truncation", err)
	}
}
