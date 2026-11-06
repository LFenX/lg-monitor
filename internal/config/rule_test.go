package config

import (
	"math"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAdditionalRulesValidateReferencesAndFields(t *testing.T) {
	for _, tc := range []struct{ name, rule, want string }{
		{"global metric", "{id: metric, type: metric_threshold, metric: system.cpu_percent, op: gt, threshold: 90}", ""},
		{"service", "{id: service, type: service_stopped, target: demo}", ""},
		{"script", "{id: script, type: script_failed, target: demo}", ""},
		{"SQL", "{id: query, type: sql, query: \"SELECT 'x' AS key\", interval: 1m}", ""},
		{"process target required", "{id: metric, type: metric_threshold, metric: process.cpu_percent, op: gt, threshold: 90}", "目标"},
		{"threshold required", "{id: metric, type: metric_threshold, metric: system.cpu_percent, op: gt}", "threshold"},
		{"mixed fields", "{id: service, type: service_stopped, target: demo, query: 'SELECT 1'}", "只用于 sql"},
		{"invalid reminder", "{id: service, type: service_stopped, target: demo, renotify: 0s}", "renotify"},
		{"invalid SQL", "{id: query, type: sql, query: 'SELECT 1; SELECT 2', interval: 1m}", "一条语句"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := fstest.MapFS{"targets.yaml": {Data: []byte("targets: [{id: demo, match: {service: fake}, probes: [{type: script, script: fake.ps1, interval: 1m, timeout: 1s}]}]")}, "rules.yaml": {Data: []byte("rules:\n- " + strings.TrimSuffix(tc.rule, "}") + ", severity: warning, notify: [fake]}\n")}, "notifiers.yaml": {Data: []byte("feishuApps: [{id: fake, appId: cli_fake, secretRef: fake, domain: feishu}]\nnotifiers: [{id: fake, type: feishu_app, app: fake, chatId: oc_fake}]")}}
			_, err := Load(files)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("additional rule validation: %v want %q", err, tc.want)
			}
		})
	}
	value := math.Inf(1)
	if err := (Rule{Type: "metric_threshold", Metric: "system.cpu_percent", Op: "gt", Threshold: &value}).ValidateCondition(Target{}); err == nil {
		t.Fatal("infinite threshold accepted")
	}
}

func TestAdditionalRuleSnapshotOwnsPointerFields(t *testing.T) {
	value, probe := 90.0, 0
	cfg := &Config{Rules: []Rule{{Threshold: &value, Probe: &probe}}}
	copy := cfg.clone()
	*copy.Rules[0].Threshold, *copy.Rules[0].Probe = 1, 3
	if value != 90 || probe != 0 {
		t.Fatal("rule snapshot aliases producer pointers")
	}
}
