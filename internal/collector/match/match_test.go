package match

import (
	"reflect"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
)

func port(number int) *int { return &number }

func TestMatching(t *testing.T) {
	processes := []collector.Process{
		{PID: 20, CreatedAt: 100, Name: "NODE.EXE", Cmdline: "node.exe demo-server"},
		{PID: 10, CreatedAt: 101, Name: "node.exe", Cmdline: "node.exe other-server"},
		{PID: 30, CreatedAt: 102, Name: "demo.exe", Cmdline: "demo.exe --serve"},
	}
	ports := []collector.Port{
		{PID: 20, LocalAddress: "127.0.0.1", Port: 3001},
		{PID: 20, LocalAddress: "::1", Port: 3001},
		{PID: 10, LocalAddress: "0.0.0.0", Port: 3002},
		{PID: 30, LocalAddress: "127.0.0.1", Port: 3003},
	}
	cases := []struct {
		name   string
		match  config.Match
		pids   []int32
		ports  int
		reason string
	}{
		{"name only and multiple matches", config.Match{Process: "node.exe"}, []int32{10, 20}, 3, "匹配到 2 个进程"},
		{"name and command", config.Match{Process: "node.exe", Cmdline: `demo-server$`}, []int32{20}, 2, "匹配到 1 个进程"},
		{"name and port", config.Match{Process: "node.exe", Port: port(3001)}, []int32{20}, 2, "匹配到 1 个进程"},
		{"port owned by another process", config.Match{Process: "node.exe", Port: port(3003)}, nil, 0, "没有在监听 3003"},
		{"no match", config.Match{Process: "absent.exe"}, nil, 0, "没有找到匹配的进程"},
		{"command mismatch", config.Match{Process: "node.exe", Cmdline: "missing"}, nil, 0, "进程名匹配，但命令行不匹配"},
		{"all conditions on same process", config.Match{Process: "node.exe", Cmdline: "other-server", Port: port(3001)}, nil, 0, "没有在监听 3001"},
		{"port only", config.Match{Port: port(3003)}, []int32{30}, 1, "匹配到 1 个进程"},
		{"command only", config.Match{Cmdline: "--serve$"}, []int32{30}, 1, "匹配到 1 个进程"},
		{"service deferred", config.Match{Process: "node.exe", Service: "demo-service"}, nil, 0, "M1-07"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matcher, err := New([]config.Target{{ID: "demo", Match: tc.match}})
			if err != nil {
				t.Fatal(err)
			}
			results := matcher.Match(processes, ports)
			if len(results) != 1 || results[0].TargetID != "demo" {
				t.Fatalf("results = %+v", results)
			}
			result := results[0]
			var pids []int32
			for _, process := range result.Processes {
				pids = append(pids, process.PID)
			}
			wantState := "down"
			if len(tc.pids) > 0 {
				wantState = "up"
			}
			if tc.match.Service != "" {
				wantState = collector.StateUnknown
			}
			if !reflect.DeepEqual(pids, tc.pids) || len(result.Ports) != tc.ports || result.State != wantState || !strings.Contains(result.Reason, tc.reason) {
				t.Fatalf("result = %+v, pids %v; want pids %v, %d ports, state %s, reason %q", result, pids, tc.pids, tc.ports, wantState, tc.reason)
			}
		})
	}
}

func TestUnknownMatchingConditions(t *testing.T) {
	unavailable := collector.Process{PID: 10, CreatedAt: 1, Name: "postgres.exe", CmdlineUnavailable: true}
	for _, test := range []struct {
		name      string
		match     config.Match
		processes []collector.Process
		ports     []collector.Port
		state     string
		reason    string
	}{
		{"service only", config.Match{Service: "postgres"}, nil, nil, collector.StateUnknown, "请暂时使用"},
		{"unavailable command", config.Match{Process: "postgres.exe", Cmdline: "database"}, []collector.Process{unavailable}, nil, collector.StateUnknown, "权限不足"},
		{"complete match wins", config.Match{Process: "postgres.exe", Cmdline: "database"}, []collector.Process{unavailable, {PID: 11, Name: "postgres.exe", Cmdline: "postgres database"}}, nil, collector.StateUp, "匹配到 1 个进程"},
		{"wrong process name", config.Match{Process: "node.exe", Cmdline: "database"}, []collector.Process{unavailable}, nil, collector.StateDown, "没有找到"},
		{"port disproves uncertain command", config.Match{Process: "postgres.exe", Cmdline: "database", Port: port(5432)}, []collector.Process{unavailable}, []collector.Port{{PID: 11, Port: 5432}}, collector.StateDown, "没有在监听"},
		{"matching port uncertain command", config.Match{Cmdline: "database", Port: port(5432)}, []collector.Process{unavailable}, []collector.Port{{PID: 10, Port: 5432}}, collector.StateUnknown, "权限不足"},
		{"readable empty command", config.Match{Cmdline: "database"}, []collector.Process{{PID: 10, Name: "postgres.exe"}}, nil, collector.StateDown, "命令行不匹配"},
	} {
		t.Run(test.name, func(t *testing.T) {
			matcher, err := New([]config.Target{{ID: "demo", Match: test.match}})
			if err != nil {
				t.Fatal(err)
			}
			result := matcher.Match(test.processes, test.ports)[0]
			if result.State != test.state || !strings.Contains(result.Reason, test.reason) {
				t.Fatalf("Match() = %+v, want %s containing %q", result, test.state, test.reason)
			}
		})
	}
}

func TestTargetIdentitySurvivesProcessRestart(t *testing.T) {
	matcher, err := New([]config.Target{{ID: "stable", Match: config.Match{Process: "demo.exe"}}})
	if err != nil {
		t.Fatal(err)
	}
	before := matcher.Match([]collector.Process{{PID: 42, CreatedAt: 1, Name: "demo.exe"}}, nil)[0]
	after := matcher.Match([]collector.Process{{PID: 52, CreatedAt: 2, Name: "demo.exe"}}, nil)[0]
	if before.TargetID != after.TargetID || before.State != "up" || after.State != "up" || before.Processes[0].Identity() == after.Processes[0].Identity() {
		t.Fatal("target identity was coupled to its process identity")
	}
}

func TestInvalidMatchingConfiguration(t *testing.T) {
	for _, targets := range [][]config.Target{
		{{ID: "empty"}},
		{{ID: "regexp", Match: config.Match{Cmdline: "["}}},
		{{ID: "port", Match: config.Match{Port: port(0)}}},
		{{ID: "port", Match: config.Match{Port: port(65536)}}},
		{{Match: config.Match{Process: "demo.exe"}}},
		{{ID: "same", Match: config.Match{Process: "demo.exe"}}, {ID: "same", Match: config.Match{Process: "other.exe"}}},
	} {
		if _, err := New(targets); err == nil {
			t.Fatalf("invalid configuration was accepted: %+v", targets)
		}
	}
}

func TestEmptySnapshotsAndConfigurationIsolation(t *testing.T) {
	configuredPort := 3001
	matcher, err := New([]config.Target{{ID: "demo", Match: config.Match{Port: &configuredPort}}})
	if err != nil {
		t.Fatal(err)
	}
	configuredPort = 3002
	if result := matcher.Match(nil, []collector.Port{{PID: 99, Port: 3001}})[0]; result.State != collector.StateUp || len(result.Ports) != 1 || !strings.Contains(result.Reason, "进程信息不可用") {
		t.Fatalf("a listening port with unavailable process information was considered down: %+v", result)
	}
	result := matcher.Match([]collector.Process{{PID: 42, CreatedAt: 1}}, []collector.Port{{PID: 42, Port: 3001}})[0]
	if result.State != "up" {
		t.Fatal("caller mutation changed the compiled matcher")
	}
}

func TestSystemOwnedPortWithoutProcessSnapshot(t *testing.T) {
	matcher, err := New([]config.Target{{ID: "system-web", Match: config.Match{Port: port(3001)}}})
	if err != nil {
		t.Fatal(err)
	}
	result := matcher.Match(nil, []collector.Port{{PID: 4, LocalAddress: "0.0.0.0", Port: 3001}})[0]
	if result.State != collector.StateUp || len(result.Processes) != 0 || len(result.Ports) != 1 || result.Reason != "端口 3001 由 PID 4 监听，进程信息不可用" {
		t.Fatalf("system listener = %+v", result)
	}
}
