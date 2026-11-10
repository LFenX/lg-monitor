package blueprint

import (
	"slices"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/config"
)

func TestEveryTemplateBuildsAValidDashboard(t *testing.T) {
	templates := Templates()
	if len(templates) < 7 {
		t.Fatalf("templates=%d", len(templates))
	}
	seen := map[string]bool{}
	for _, template := range templates {
		if seen[template.ID] || template.Title == "" || template.Description == "" || template.Example == "" || len(template.Panels) == 0 {
			t.Fatalf("incomplete template %+v", template)
		}
		seen[template.ID] = true
		input := TemplateInput{Template: template.ID}
		if template.NeedsTarget {
			input.Target = "fake-target"
		}
		page, err := Build(input, func(string) bool { return true })
		if err != nil {
			t.Fatalf("%s: %v", template.ID, err)
		}
		if issues := config.CheckDashboard(page); len(issues) > 0 {
			t.Fatalf("%s: %v", template.ID, issues)
		}
		width := 0
		for _, panel := range page.Panels {
			width += panel.Width
			if template.NeedsTarget && !strings.Contains(panel.Query, "'fake-target'") {
				t.Fatalf("%s/%s ignores its target", template.ID, panel.ID)
			}
		}
		// Rows of twelve keep every template free of ragged gaps on wide screens.
		if width%12 != 0 {
			t.Fatalf("%s widths sum to %d", template.ID, width)
		}
	}
}

func TestTemplateInputsAreCheckedBeforeBuilding(t *testing.T) {
	for _, input := range []TemplateInput{
		{Template: "missing"},
		{Template: "service-health"},
		{Template: "service-health", Target: "BAD ID"},
		{Template: "pc-overview", Target: "web"},
		{Template: "pc-overview", Theme: "neon"},
	} {
		if _, err := Build(input, func(string) bool { return true }); err == nil {
			t.Fatalf("accepted %+v", input)
		}
	}
	if _, err := Build(TemplateInput{Template: "website", Target: "web"}, func(string) bool { return false }); err == nil || !strings.Contains(err.Error(), "list_monitors") {
		t.Fatalf("unknown target error=%v", err)
	}
	page, err := Build(TemplateInput{Template: "website", Target: "web", Theme: "paper"}, nil)
	if err != nil || page.ID != "web" || page.Theme != "paper" || !strings.Contains(page.Title, "web") {
		t.Fatalf("page=%+v %v", page, err)
	}
}

func TestPlanTurnsOneIntentIntoTargetAndRules(t *testing.T) {
	port := 3000
	cases := []struct {
		input    MonitorInput
		id       string
		rules    []string
		template string
	}{
		{MonitorInput{Name: "博客", Port: &port}, "port-3000", []string{"unreachable"}, "service-health"},
		{MonitorInput{Name: "Shop Site", URL: "https://shop.example.test/"}, "shop-site", []string{"cert-expiry", "unreachable"}, "website"},
		{MonitorInput{Name: "数据库", Service: "postgresql-x64-17"}, "postgresql-x64-17", []string{"service-stopped"}, "service-health"},
		{MonitorInput{Name: "nginx", Process: "nginx.exe", Port: &port}, "nginx", []string{"process-down", "unreachable"}, "service-health"},
	}
	for _, item := range cases {
		plan, err := Plan(item.input, []string{"fake-channel"})
		if err != nil {
			t.Fatalf("%+v: %v", item.input, err)
		}
		var suffixes []string
		for _, rule := range plan.Rules {
			if rule.Target != plan.Target.ID || !slices.Equal(rule.Notify, []string{"fake-channel"}) {
				t.Fatalf("rule %+v", rule)
			}
			suffixes = append(suffixes, strings.TrimPrefix(rule.ID, plan.Target.ID+"-"))
		}
		slices.Sort(suffixes)
		if plan.Target.ID != item.id || !slices.Equal(suffixes, item.rules) || plan.Template != item.template {
			t.Fatalf("%+v → id=%s rules=%v template=%s", item.input, plan.Target.ID, suffixes, plan.Template)
		}
		if plan.Target.Match == (config.Match{}) && item.input.URL == "" {
			t.Fatal("local monitor without match")
		}
	}
	external, err := Plan(MonitorInput{Name: "Shop", URL: "https://shop.example.test/"}, []string{"fake-channel"})
	if err != nil || !ProbeOnly(external.Target) {
		t.Fatalf("external URL plan=%+v %v", external.Target, err)
	}
	local, err := Plan(MonitorInput{Name: "Local", URL: "http://127.0.0.1:3000/health"}, []string{"fake-channel"})
	if err != nil || ProbeOnly(local.Target) || local.Target.Match.Port == nil || *local.Target.Match.Port != 3000 {
		t.Fatalf("loopback URL plan=%+v %v", local.Target, err)
	}
	for _, input := range []MonitorInput{
		{Port: &port},
		{Name: "空"},
		{Name: "坏网址", URL: "ftp://example.test"},
		{Name: "只有命令行", Cmdline: "blog"},
		{Name: "坏级别", Port: &port, Severity: "fatal"},
		{Name: "坏时长", Port: &port, For: "soon"},
	} {
		if _, err := Plan(input, []string{"fake-channel"}); err == nil {
			t.Fatalf("accepted %+v", input)
		}
	}
}
