package rules

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/collector/match"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestProcessAndPortRulesUseIndependentFacts(t *testing.T) {
	port := 3001
	process := collector.Process{PID: 42, CreatedAt: 1, Name: "demo.exe", Cmdline: "demo --serve"}
	unknown := process
	unknown.Cmdline, unknown.CmdlineUnavailable = "", true
	for _, test := range []struct {
		name          string
		match         config.Match
		processes     []collector.Process
		ports         []collector.Port
		process, port string
		want          []string
		unknownRules  bool
	}{
		{"healthy", config.Match{Process: "demo.exe", Port: &port}, []collector.Process{process}, []collector.Port{{PID: 42, Port: 3001}}, "found", "listening", []string{}, false},
		{"port closed only", config.Match{Process: "demo.exe", Port: &port}, []collector.Process{process}, nil, "found", "closed", []string{"port"}, false},
		{"process missing only", config.Match{Process: "demo.exe", Port: &port}, nil, nil, "missing", "closed", []string{"process"}, false},
		{"port only", config.Match{Port: &port}, nil, nil, "", "closed", []string{"port"}, false},
		{"unreadable command with listener", config.Match{Process: "demo.exe", Cmdline: "serve", Port: &port}, []collector.Process{unknown}, []collector.Port{{PID: 42, Port: 3001}}, "unknown", "unknown", []string{}, true},
		{"unreadable command without listener", config.Match{Process: "demo.exe", Cmdline: "serve", Port: &port}, []collector.Process{unknown}, nil, "unknown", "closed", []string{}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := config.Target{ID: "demo", Match: test.match}
			matcher, err := match.New([]config.Target{target})
			if err != nil {
				t.Fatal(err)
			}
			result := matcher.Match(test.processes, test.ports)[0]
			if result.Process != test.process || result.Port != test.port {
				t.Fatalf("component facts: %+v", result)
			}
			cfg := &config.Config{Targets: []config.Target{target}, Rules: []config.Rule{{ID: "port", Target: "demo", Type: "port_closed", Severity: "warning"}}}
			if test.match.Process != "" || test.match.Cmdline != "" {
				cfg.Rules = append(cfg.Rules, config.Rule{ID: "process", Target: "demo", Type: "process_absent", Severity: "critical"})
			}
			repository := &memoryRepository{alerts: make(map[string]Alert)}
			queue := &notificationQueue{alerts: make(chan Alert, 10)}
			engine, err := New(context.Background(), Options{Config: cfg, Repository: repository, Notifications: queue, Now: func() time.Time { return time.UnixMilli(1000) }})
			if err != nil {
				t.Fatal(err)
			}
			message := pipeline.Message{CollectorID: "fake", Seq: 1, TS: 1000, Kind: pipeline.KindTargetStatus,
				TargetStatus: &pipeline.TargetStatus{TargetID: "demo", State: result.State, Process: result.Process, Port: result.Port}}
			if err := engine.Handle(context.Background(), message); err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, alert := range repository.alerts {
				if alert.State == StateFiring {
					got = append(got, alert.RuleID)
				}
			}
			sort.Strings(got)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("fired=%v want=%v", got, test.want)
			}
			if test.unknownRules {
				for _, spec := range engine.specs {
					if _, known, _ := engine.condition(spec, "fake"); known {
						t.Fatalf("%s treated unavailable command line as known", spec.rule.ID)
					}
				}
			}
		})
	}
}
