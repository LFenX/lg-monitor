package config

import (
	"strings"
	"testing"
)

func TestOfflinePolicyDefaultsAndExplicitGlobalRuleValidation(t *testing.T) {
	cfg := &Config{Notifiers: []Notifier{{ID: "first"}, {ID: "second"}}}
	defaults := OfflineRules(cfg)
	if len(defaults) != 1 || defaults[0].For != "30s" || len(defaults[0].Notify) != 2 || defaults[0].Target != "" {
		t.Fatalf("wrong automatic policy: %+v", defaults)
	}
	files := validFiles()
	files["rules.yaml"].Data = []byte("rules:\n- {id: offline, type: collector_offline, severity: warning, notify: [alerts], template: oncall}\n")
	loaded, err := Load(files)
	if err != nil || OfflineRules(loaded)[0].ID != "offline" || OfflineRules(loaded)[0].For != "30s" {
		t.Fatalf("global rule/default delay: %v", err)
	}
	files["rules.yaml"].Data = []byte("rules:\n- {id: offline, type: collector_offline, target: web, severity: warning, notify: [alerts]}\n")
	if _, err := Load(files); err == nil || !strings.Contains(err.Error(), "删除 target") {
		t.Fatalf("accepted per-target offline rule: %v", err)
	}
	files["rules.yaml"].Data = []byte("rules:\n- {id: collector-offline, type: process_absent, target: web, severity: warning, notify: [alerts]}\n")
	if _, err := Load(files); err == nil || !strings.Contains(err.Error(), "保留") {
		t.Fatalf("accepted reserved ID collision: %v", err)
	}
}
