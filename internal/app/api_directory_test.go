package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/transport"
)

type apiConfigurationPresence struct {
	state transport.CollectorConfigState
}

func (s *apiConfigurationPresence) Collectors(context.Context) ([]rules.CollectorPresence, error) {
	return []rules.CollectorPresence{{ID: "fake-host", Hostname: "fake host", LastSeen: 2000000000000, Connected: true}}, nil
}
func (s *apiConfigurationPresence) ConfigState(string) transport.CollectorConfigState { return s.state }

func TestHTTPCollectorConfigurationProgressRemainsReadableAndExact(t *testing.T) {
	source := &apiConfigurationPresence{}
	f := newAPIFixtureWithProcessSource(t, time.UnixMilli(2000000000000), nil, nil, source)
	for _, state := range []string{"pending", "applied", "rejected", "outdated"} {
		source.state = transport.CollectorConfigState{State: state, Version: "fake-content-hash", Message: "fake " + state}
		body := f.request(t, "GET", "/api/v1/collectors", "read", "", 200).Body.Bytes()
		var rows []api.Collector
		if err := json.Unmarshal(body, &rows); err != nil || len(rows) != 1 || rows[0].Config.State != state ||
			rows[0].Config.Version != source.state.Version || rows[0].Config.Message != source.state.Message {
			t.Fatalf("config progress changed at HTTP boundary: %s %v", body, err)
		}
	}
}

func TestHTTPWorkingDirectoryAppearsOnlyInDirectoryDraftResultAndSchema(t *testing.T) {
	source := processSourceFunc(func(context.Context, string, *config.Match) (collector.ProcessList, error) {
		return collector.ProcessList{Collector: collector.ProcessCollector{ID: "fake-host"}, CollectedAt: 2000000000000,
			Processes: []collector.ProcessInfo{{PID: 42, CreatedAt: 1, Name: "node.exe", Exe: `D:\tools\node.exe`, WorkDir: `D:\site\blog`, Ports: []collector.ProcessPort{}, Services: []string{}}}}, nil
	})
	f := newAPIFixtureWithProcessSource(t, time.UnixMilli(2000000000000), nil, source)
	list := f.request(t, "GET", "/api/v1/collectors/fake-host/processes", "read", "", 200).Body.String()
	if strings.Contains(list, "workDir") {
		t.Fatalf("ordinary list exposed working directory: %s", list)
	}
	f.now = f.now.Add(2 * time.Second)
	draft := f.request(t, "POST", "/api/v1/collectors/fake-host/processes/match", "config", `{"match":{"process":"node","workDir":"D:\\site\\blog"}}`, 200).Body.Bytes()
	var matched api.TargetMatchResult
	if err := json.Unmarshal(draft, &matched); err != nil || len(matched.Processes) != 1 || matched.Processes[0].WorkDir != `D:\site\blog` {
		t.Fatalf("directory draft omitted working directory: %s %v", draft, err)
	}
	f.now = f.now.Add(2 * time.Second)
	exeOnly := f.request(t, "POST", "/api/v1/collectors/fake-host/processes/match", "config", `{"match":{"exeDir":"D:\\tools"}}`, 200).Body.String()
	if strings.Contains(exeOnly, "workDir") {
		t.Fatalf("exeDir-only draft exposed working directory: %s", exeOnly)
	}
	paths := f.spec["paths"].(map[string]any)
	processProperties := func(path, method string) map[string]any {
		operation := paths[path].(map[string]any)[method].(map[string]any)
		responses := operation["responses"].(map[string]any)["200"].(map[string]any)
		schema := responses["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
		return schema["properties"].(map[string]any)["processes"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	}
	if _, exposed := processProperties("/api/v1/collectors/{id}/processes", "get")["workDir"]; exposed {
		t.Fatal("ordinary process list schema advertises working directory")
	}
	if _, described := processProperties("/api/v1/collectors/{id}/processes/match", "post")["workDir"]; !described {
		t.Fatal("draft match schema omitted working directory")
	}
}
