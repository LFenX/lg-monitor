package match

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
)

type fakeDirectories struct {
	calls []collector.ProcessIdentity
	value string
	err   error
}

func (f *fakeDirectories) WorkingDirectory(_ context.Context, identity collector.ProcessIdentity) (string, error) {
	f.calls = append(f.calls, identity)
	return f.value, f.err
}

func TestDirectoryMatchingUsesSegmentsAndUnknownFacts(t *testing.T) {
	for _, test := range []struct {
		name, exe, cwd, state string
		spec                  config.Match
	}{
		{"exe directory", `\\?\D:\Tools\bin\node.exe`, "", "up", config.Match{ExeDir: `d:/tools/`}},
		{"exe boundary", `D:\ToolsOther\node.exe`, "", "down", config.Match{ExeDir: `D:\Tools`}},
		{"work directory", `D:\Tools\node.exe`, `\\?\D:\SITE\a\child\`, "up", config.Match{Process: "node", WorkDir: `d:/site/./a`}},
		{"UNC work directory", `D:\Tools\node.exe`, `\\?\UNC\server\share\blog\child`, "up", config.Match{Process: "node", WorkDir: `\\SERVER\SHARE\blog`}},
		{"work boundary", `D:\Tools\node.exe`, `D:\site\ab`, "down", config.Match{Process: "node", WorkDir: `D:\site\a`}},
		{"exe unreadable", "", "", "unknown", config.Match{ExeDir: `D:\Tools`}},
		{"cwd unreadable", `D:\Tools\node.exe`, "", "unknown", config.Match{Process: "node", WorkDir: `D:\site\a`}},
		{"other known condition rejects", `D:\other\node.exe`, "", "down", config.Match{ExeDir: `D:\Tools`, WorkDir: `D:\site\a`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			matcher, err := New([]config.Target{{ID: "blog", Match: test.spec}})
			if err != nil {
				t.Fatal(err)
			}
			result := matcher.Match([]collector.Process{{PID: 42, CreatedAt: 1, Name: "node.exe", Exe: test.exe, WorkDir: test.cwd}}, nil)[0]
			if result.State != test.state {
				t.Fatalf("directory result: %+v", result)
			}
			if test.state == "unknown" && (result.Process != collector.ProcessUnknown || !strings.Contains(result.Reason, "读取不到")) {
				t.Fatalf("unreadable directory became missing: %+v", result)
			}
		})
	}
}

func TestResolveWorkingDirectoriesOnlyReadsFullyQualifiedCandidates(t *testing.T) {
	rule := config.Match{Process: "node", Cmdline: "serve", ExeDir: `D:\tools`, WorkDir: `D:\site`, Port: port(8080)}
	matcher, err := New([]config.Target{{ID: "blog", Match: rule}, {ID: "second", Match: rule}})
	if err != nil {
		t.Fatal(err)
	}
	processes := []collector.Process{
		{PID: 1, CreatedAt: 1, Name: "python.exe", Cmdline: "serve", Exe: `D:\tools\python.exe`},
		{PID: 2, CreatedAt: 2, Name: "node.exe", Cmdline: "other", Exe: `D:\tools\node.exe`},
		{PID: 3, CreatedAt: 3, Name: "node.exe", Cmdline: "serve", Exe: `D:\other\node.exe`},
		{PID: 4, CreatedAt: 4, Name: "node.exe", Cmdline: "serve"},
		{PID: 5, CreatedAt: 5, Name: "node.exe", CmdlineUnavailable: true, Exe: `D:\tools\node.exe`},
		{PID: 6, CreatedAt: 6, Name: "node.exe", Cmdline: "serve", Exe: `D:\tools\node.exe`},
		{PID: 20, CreatedAt: 20, Name: "node.exe", Cmdline: "serve", Exe: `D:\tools\node.exe`},
	}
	var ports []collector.Port
	for _, pid := range []int32{1, 2, 3, 4, 5, 20} {
		ports = append(ports, collector.Port{PID: pid, Port: 8080})
	}
	source := &fakeDirectories{value: `D:\site\blog`}
	resolved, err := matcher.ResolveDirectories(context.Background(), collector.ProcessSnapshot{Processes: processes, Ports: ports}, source)
	if err != nil || len(source.calls) != 1 || source.calls[0].PID != 20 {
		t.Fatalf("unqualified processes read: %+v %v", source.calls, err)
	}
	for _, process := range processes {
		if process.WorkDir != "" {
			t.Fatal("shared process snapshot was modified")
		}
	}
	for _, result := range matcher.Match(resolved, ports) {
		if result.State != "up" || len(result.Processes) != 1 || result.Processes[0].PID != 20 || result.Processes[0].WorkDir != source.value {
			t.Fatalf("candidate match failed: %+v", result)
		}
	}
	source.err = errors.New("fake denied")
	resolved, err = matcher.ResolveDirectories(context.Background(), collector.ProcessSnapshot{Processes: processes, Ports: ports}, source)
	if err != nil {
		t.Fatal(err)
	}
	if result := matcher.Match(resolved, ports)[0]; result.State != "unknown" || result.Process == collector.ProcessMissing {
		t.Fatalf("denied directory triggered missing: %+v", result)
	}
	serviceRule := rule
	serviceRule.Service = "fake-service"
	serviceMatcher, _ := New([]config.Target{{ID: "service", Match: serviceRule}})
	source.calls = nil
	snapshot := collector.ProcessSnapshot{Processes: processes, Ports: ports, ServicesKnown: true, Services: []collector.Service{{Name: "fake-service", State: collector.ServiceStopped, PID: 20}}}
	if _, err := serviceMatcher.ResolveDirectories(context.Background(), snapshot, source); err != nil || len(source.calls) != 0 {
		t.Fatal("stopped service read a working directory")
	}
}
