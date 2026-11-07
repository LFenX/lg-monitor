package config

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestNotifierFilterCombinations(t *testing.T) {
	for _, tc := range []struct {
		name                string
		filter              *NotifierFilter
		tags                []string
		collector, hostname string
		want                bool
	}{
		{"omitted", nil, nil, "node-01", "WEB-01", true},
		{"empty object", &NotifierFilter{}, nil, "node-01", "WEB-01", true},
		{"severity OR", &NotifierFilter{Severity: []string{"warning", "critical"}}, nil, "node-01", "WEB-01", true},
		{"severity mismatch", &NotifierFilter{Severity: []string{"warning"}}, nil, "node-01", "WEB-01", false},
		{"tag OR", &NotifierFilter{Tags: []string{"api", "web"}}, []string{"web"}, "node-01", "WEB-01", true},
		{"tag case", &NotifierFilter{Tags: []string{"WEB"}}, []string{"web"}, "node-01", "WEB-01", false},
		{"tagless", &NotifierFilter{Tags: []string{"web"}}, nil, "node-01", "WEB-01", false},
		{"ID", &NotifierFilter{Collectors: []string{"node-01"}}, nil, "node-01", "WEB-01", true},
		{"ID exact", &NotifierFilter{Collectors: []string{"NODE-01"}}, nil, "node-01", "", false},
		{"hostname case", &NotifierFilter{Collectors: []string{"web-01"}}, nil, "node-01", "WEB-01", true},
		{"AND", &NotifierFilter{Severity: []string{"critical"}, Tags: []string{"api", "web"}, Collectors: []string{"other", "web-01"}}, []string{"web"}, "node-01", "WEB-01", true},
		{"AND fails", &NotifierFilter{Severity: []string{"critical"}, Tags: []string{"api"}, Collectors: []string{"web-01"}}, []string{"web"}, "node-01", "WEB-01", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.filter.Match("critical", tc.collector, tc.hostname, tc.tags); got != tc.want {
				t.Fatalf("match=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestNotifierFilterValidationAndSnapshotIsolation(t *testing.T) {
	for _, field := range []string{"severity", "tags", "collectors"} {
		files := validFiles()
		files["notifiers.yaml"].Data = append(files["notifiers.yaml"].Data, []byte("    filter: {"+field+": []}\n")...)
		if _, err := Load(files); err == nil || !strings.Contains(err.Error(), "不限制就不要写这一项") {
			t.Fatalf("%s: %v", field, err)
		}
	}
	for _, filter := range []string{"null", "{tags: null}", "{severity: [fatal]}", "{collectors: ['*']}", "{tags: ['']}", "{tags: [web, web]}"} {
		files := validFiles()
		files["notifiers.yaml"].Data = append(files["notifiers.yaml"].Data, []byte("    filter: "+filter+"\n")...)
		if _, err := Load(files); err == nil {
			t.Fatalf("accepted %s", filter)
		}
	}
	files := validFiles()
	files["notifiers.yaml"].Data = append(files["notifiers.yaml"].Data, []byte("    filter: {severity: [warning]}\n")...)
	if _, err := Load(files); err == nil || !strings.Contains(err.Error(), "永远不会发到") {
		t.Fatalf("severity conflict: %v", err)
	}
	// Generated offline rules are intentionally excluded from the explicit-rule conflict check.
	files["rules.yaml"] = &fstest.MapFile{Data: []byte("rules: []\n")}
	manager, err := NewManager(FSLoader{FS: files}, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := manager.Snapshot()
	first.Notifiers[0].Filter.Severity[0] = "critical"
	first.Notifiers[0].Filter.Tags = []string{"changed"}
	second := manager.Snapshot()
	if second.Notifiers[0].Filter.Severity[0] != "warning" || second.Notifiers[0].Filter.Tags != nil {
		t.Fatal("snapshot shared filter storage")
	}
}
