package config

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestEventLogConfigurationsAndRuleConditions(t *testing.T) {
	for _, tc := range []struct{ name, target, rule, want string }{
		{"subscription only", "{id: log, eventlog: {channel: Application, provider: Fake, ids: [1,2], levels: [error,critical]}}", "{id: crash, type: eventlog, target: log}", ""},
		{"subject preset", "{id: log, match: {process: fake.exe}}", "{id: crash, type: eventlog, target: log, preset: app_crash, count: 2, window: 1h}", ""},
		{"global preset", "{id: log, match: {process: fake.exe}}", "{id: crash, type: eventlog, preset: unexpected_shutdown}", ""},
		{"security rejected", "{id: log, eventlog: {channel: Security, provider: Fake}}", "", "Application 或 System"},
		{"duplicate ids", "{id: log, eventlog: {channel: Application, provider: Fake, ids: [1,1]}}", "", "不重复"},
		{"oversize ids", "{id: log, eventlog: {channel: Application, provider: Fake, ids: [65536]}}", "", "65535"},
		{"invalid level", "{id: log, eventlog: {channel: Application, provider: Fake, levels: [fatal]}}", "", "levels"},
		{"no subscription", "{id: log, match: {process: fake}}", "{id: crash, type: eventlog, target: log}", "订阅"},
		{"incorrect subject", "{id: log, match: {service: fake}}", "{id: crash, type: eventlog, target: log, preset: app_crash}", "match.process"},
		{"instantaneous", "{id: log, match: {process: fake}}", "{id: crash, type: eventlog, preset: app_crash, for: 0s}", "不接受 for"},
		{"count positive", "{id: log, match: {process: fake}}", "{id: crash, type: eventlog, preset: app_crash, count: 0}", "count"},
		{"window bound", "{id: log, match: {process: fake}}", "{id: crash, type: eventlog, preset: app_crash, window: 25h}", "24h"},
		{"other rule rejects fields", "{id: log, match: {process: fake}}", "{id: crash, type: process_absent, target: log, count: 1}", "只用于 eventlog"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := fstest.MapFS{"targets.yaml": {Data: []byte("targets: [" + tc.target + "]")}}
			if tc.rule != "" {
				rule := strings.TrimSuffix(tc.rule, "}") + ", severity: warning, notify: [fake]}"
				files["rules.yaml"] = &fstest.MapFile{Data: []byte("rules: [" + rule + "]")}
				files["notifiers.yaml"] = &fstest.MapFile{Data: []byte("notifiers: [{id: fake, type: toast}]")}
			}
			_, err := Load(files)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}

func TestEventLogConfigurationSnapshotOwnsSlicesAndCount(t *testing.T) {
	count := 2
	cfg := &Config{Targets: []Target{{EventLog: &EventLogSubscription{IDs: []int{1}, Levels: []string{"error"}}}}, Rules: []Rule{{Count: &count}}}
	copy := cfg.clone()
	copy.Targets[0].EventLog.IDs[0] = 2
	copy.Targets[0].EventLog.Levels[0] = "critical"
	*copy.Rules[0].Count = 3
	if cfg.Targets[0].EventLog.IDs[0] != 1 || cfg.Targets[0].EventLog.Levels[0] != "error" || count != 2 {
		t.Fatal("snapshot shares mutable event log fields")
	}
}
