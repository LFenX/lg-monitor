package cards

import (
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func TestCertificateTemplateDataUsesLocalDateAndOnlyCertificateRules(t *testing.T) {
	renderer, _, _ := testRenderer(t, config.Template{ID: "certificate", Extends: "standard",
		Title: "{{if .Cert}}证书 {{.Cert.subject}} {{.Cert.notAfter}} {{.Cert.daysLeft}}{{else}}普通告警{{end}}"})
	input := Sample("firing")
	input.Rule.Type = "cert_expiry"
	input.Alert.Cert = &rules.Certificate{NotAfter: renderer.now().Add(3 * 24 * time.Hour).UnixMilli(), Subject: "fake certificate"}
	data := renderer.data(input, renderer.now(), 0)
	certificate, ok := data["Cert"].(map[string]any)
	expectedDate := time.UnixMilli(input.Alert.Cert.NotAfter).In(renderer.location).Format("2006-01-02 15:04:05")
	if !ok || certificate["notAfter"] != expectedDate || certificate["subject"] != "fake certificate" || certificate["daysLeft"] != int64(3) {
		t.Fatalf("certificate data changed: %+v", data["Cert"])
	}
	rendered := renderer.Render("certificate", input)
	if rendered.Fallback || !strings.Contains(string(rendered.JSON), expectedDate) || !strings.Contains(string(rendered.JSON), "fake certificate") {
		t.Fatalf("certificate template failed: %s %+v", rendered.JSON, rendered)
	}
	input.Rule.Type = "probe_failed"
	data = renderer.data(input, renderer.now(), 0)
	if value := data["Cert"].(map[string]any); value != nil {
		t.Fatalf("ordinary rule exposed certificate: %+v", value)
	}
	rendered = renderer.Render("certificate", input)
	if rendered.Fallback || !strings.Contains(string(rendered.JSON), "普通告警") {
		t.Fatalf("optional certificate broke ordinary template: %s", rendered.JSON)
	}
}
