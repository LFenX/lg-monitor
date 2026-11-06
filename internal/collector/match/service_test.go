package match

import (
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
)

func TestServiceTargetsUseOnlyTheServiceProcess(t *testing.T) {
	processes := []collector.Process{{PID: 10, Name: "worker.exe", Cmdline: "worker --primary"}, {PID: 20, Name: "worker.exe", Cmdline: "worker --other"}}
	ports := []collector.Port{{PID: 10, Port: 6001}, {PID: 20, Port: 6002}}
	for _, tc := range []struct {
		name           string
		service        collector.Service
		match          config.Match
		state, process string
		pid            int32
	}{
		{"service only", collector.Service{Name: "Demo", State: "running", PID: 10}, config.Match{Service: "demo"}, "up", "found", 10},
		{"all on service PID", collector.Service{Name: "Demo", State: "running", PID: 10}, config.Match{Service: "demo", Process: "worker", Cmdline: "primary", Port: port(6001)}, "up", "found", 10},
		{"other PID port rejected", collector.Service{Name: "Demo", State: "running", PID: 10}, config.Match{Service: "demo", Port: port(6002)}, "down", "found", 0},
		{"other PID command rejected", collector.Service{Name: "Demo", State: "running", PID: 10}, config.Match{Service: "demo", Cmdline: "other"}, "down", "missing", 0},
		{"stopped", collector.Service{Name: "Demo", State: "stopped"}, config.Match{Service: "demo"}, "down", "missing", 0},
		{"missing", collector.Service{Name: "Demo", State: "missing"}, config.Match{Service: "demo"}, "down", "missing", 0},
		{"permission failure", collector.Service{Name: "Demo", State: "unknown"}, config.Match{Service: "demo"}, "unknown", "unknown", 0},
		{"transition", collector.Service{Name: "Demo", State: "start_pending"}, config.Match{Service: "demo"}, "unknown", "unknown", 0},
		{"running without process details", collector.Service{Name: "Demo", State: "running", PID: 30}, config.Match{Service: "demo"}, "up", "unknown", 0},
		{"additional condition without details", collector.Service{Name: "Demo", State: "running", PID: 30}, config.Match{Service: "demo", Cmdline: "primary"}, "unknown", "unknown", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matcher, err := New([]config.Target{{ID: "fake", Match: tc.match}})
			if err != nil {
				t.Fatal(err)
			}
			result := matcher.MatchWithServices(processes, ports, []collector.Service{tc.service})[0]
			if result.State != tc.state || result.Process != tc.process || result.Service != tc.service.State {
				t.Fatalf("service match: %+v", result)
			}
			if tc.pid != 0 && (len(result.Processes) != 1 || result.Processes[0].PID != tc.pid) {
				t.Fatalf("service process missing: %+v", result)
			}
			for _, process := range result.Processes {
				if process.PID == 20 {
					t.Fatal("included a different service's process")
				}
			}
		})
	}
}
