package rules

import (
	"context"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/collector/match"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestDirectoryTargetsSupportProcessAbsentWithoutNameOrCommandLine(t *testing.T) {
	for _, directory := range []config.Match{
		{ExeDir: `D:\sites\blog`},
		{ExeDir: `D:\tools`, WorkDir: `D:\sites\blog`},
	} {
		t.Run(directory.ExeDir, func(t *testing.T) {
			target := config.Target{ID: "blog", Match: directory}
			cfg := &config.Config{Targets: []config.Target{target}, Rules: []config.Rule{
				{ID: "absent", Type: "process_absent", Target: "blog", Severity: "warning"},
			}}
			repository := &memoryRepository{alerts: map[string]Alert{}}
			queue := &notificationQueue{alerts: make(chan Alert, 10)}
			engine, err := New(context.Background(), Options{Config: cfg, Repository: repository,
				Notifications: queue, Now: func() time.Time { return time.UnixMilli(1000) }})
			if err != nil {
				t.Fatal(err)
			}
			matcher, err := match.New(cfg.Targets)
			if err != nil {
				t.Fatal(err)
			}
			executable := `D:\sites\blog\demo.exe`
			if directory.WorkDir != "" {
				executable = `D:\tools\node.exe`
			}
			observations := [][]collector.Process{
				{{PID: 42, CreatedAt: 1, Name: "demo.exe", Exe: executable, WorkDir: `D:\sites\blog`}},
				{{PID: 42, CreatedAt: 1, Name: "demo.exe"}},
				nil,
			}
			for i, processes := range observations {
				result := matcher.Match(processes, nil)[0]
				err := engine.Handle(context.Background(), pipeline.Message{
					CollectorID: "fake", Seq: uint64(i + 1), TS: 1000, Kind: pipeline.KindTargetStatus,
					TargetStatus: &pipeline.TargetStatus{TargetID: target.ID, State: result.State, Process: result.Process},
				})
				if err != nil {
					t.Fatal(err)
				}
				if i < 2 && len(repository.events) != 0 {
					t.Fatalf("present or unreadable directory fired: %+v", repository.events)
				}
			}
			if len(repository.events) != 1 || repository.events[0].Kind != StateFiring {
				t.Fatalf("known missing directory did not fire: %+v", repository.events)
			}
		})
	}
}
