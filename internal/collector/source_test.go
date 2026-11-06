package collector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	gnet "github.com/shirou/gopsutil/v4/net"
	gprocess "github.com/shirou/gopsutil/v4/process"
)

type fakeProcess struct {
	createdAt    int64
	parentPID    int32
	name         string
	cmdline      string
	exe          string
	times        *cpu.TimesStat
	memory       *gprocess.MemoryInfoStat
	threads      int32
	failures     map[string]error
	cmdlineReads int
	exeReads     int
}

func sampleProcess() *fakeProcess {
	return &fakeProcess{
		createdAt: 1700000000000, parentPID: 1, name: "demo.exe", cmdline: "demo.exe --serve", exe: `C:\demo\demo.exe`,
		times: &cpu.TimesStat{User: 1, System: 0.5}, memory: &gprocess.MemoryInfoStat{RSS: 4096}, threads: 3,
		failures: make(map[string]error),
	}
}

func (p *fakeProcess) PpidWithContext(context.Context) (int32, error) {
	return p.parentPID, p.failures["parentPid"]
}
func (p *fakeProcess) NameWithContext(context.Context) (string, error) {
	return p.name, p.failures["name"]
}
func (p *fakeProcess) CmdlineWithContext(context.Context) (string, error) {
	p.cmdlineReads++
	return p.cmdline, p.failures["cmdline"]
}
func (p *fakeProcess) ExeWithContext(context.Context) (string, error) {
	p.exeReads++
	return p.exe, p.failures["exe"]
}

func TestFiletimeToUnixMillis(t *testing.T) {
	for _, test := range []struct{ ticks, want int64 }{
		{116444736000000000, 0},
		{116444735999990000, -1},
		{133444736000000000, 1700000000000},
		{133444736000019999, 1700000000001},
	} {
		if got := filetimeToUnixMillis(test.ticks); got != test.want {
			t.Errorf("filetimeToUnixMillis(%d) = %d, want %d", test.ticks, got, test.want)
		}
	}
}

func TestProcessDetailsCache(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(fmt.Sprint(unavailable), func(t *testing.T) {
			process := sampleProcess()
			if unavailable {
				process.failures["cmdline"], process.failures["exe"] = errors.New("access denied"), errors.New("access denied")
			}
			backend := &fakeProcesses{entries: []processEntry{{pid: 42, reader: process}}}
			source := newProcessSource(backend, func() time.Time { return time.Unix(1, 0) }, nil)
			read := func() {
				t.Helper()
				rows, err := source.Snapshot(context.Background())
				if err != nil || len(rows) != 1 || rows[0].CmdlineUnavailable != unavailable {
					t.Fatalf("Snapshot() = %+v, %v", rows, err)
				}
			}
			read()
			read()
			if process.cmdlineReads != 1 || process.exeReads != 1 {
				t.Fatal("stable process details were read more than once")
			}
			backend.entries = nil
			if _, err := source.Snapshot(context.Background()); err != nil || len(source.details) != 0 {
				t.Fatalf("exited process details remained cached: %v", err)
			}
			backend.entries = []processEntry{{pid: 42, reader: process}}
			read()
			process.createdAt++
			read()
			if process.cmdlineReads != 3 || process.exeReads != 3 || len(source.details) != 1 {
				t.Fatal("exit or PID reuse failed to refresh details")
			}
		})
	}
}
func (p *fakeProcess) CreateTimeWithContext(context.Context) (int64, error) {
	return p.createdAt, p.failures["createdAt"]
}
func (p *fakeProcess) TimesWithContext(context.Context) (*cpu.TimesStat, error) {
	return p.times, p.failures["cpu"]
}
func (p *fakeProcess) MemoryInfoWithContext(context.Context) (*gprocess.MemoryInfoStat, error) {
	return p.memory, p.failures["rss"]
}
func (p *fakeProcess) NumThreadsWithContext(context.Context) (int32, error) {
	return p.threads, p.failures["threads"]
}

type fakeProcesses struct {
	entries    []processEntry
	err        error
	onRead     func()
	calls      int
	processors uint32
}

func (f *fakeProcesses) LogicalProcessors() uint32 { return max(1, f.processors) }

func TestProcessCPUUsesWholeMachineCapacity(t *testing.T) {
	process := sampleProcess()
	clock := time.Unix(1, 0)
	backend := &fakeProcesses{entries: []processEntry{{pid: 42, reader: process}}, processors: 4}
	source := newProcessSource(backend, func() time.Time { return clock }, nil)
	if _, err := source.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Second)
	process.times.User += 2
	rows, err := source.Snapshot(context.Background())
	if err != nil || len(rows) != 1 || rows[0].CPUPercent != 50 {
		t.Fatalf("Snapshot() = %+v, %v; want 50 percent of four logical processors", rows, err)
	}
	clock = clock.Add(time.Second)
	process.times.User += 5
	rows, err = source.Snapshot(context.Background())
	if err != nil || len(rows) != 1 || rows[0].CPUPercent != 100 {
		t.Fatalf("CPU percentage exceeded machine capacity: %+v, %v", rows, err)
	}
}

func (f *fakeProcesses) Processes(context.Context) ([]processEntry, error) {
	f.calls++
	if f.onRead != nil {
		f.onRead()
	}
	return f.entries, f.err
}

type fakeConnections struct {
	connections []gnet.ConnectionStat
	err         error
	onRead      func()
	kind        string
	calls       int
}

func (f *fakeConnections) Connections(_ context.Context, kind string) ([]gnet.ConnectionStat, error) {
	f.kind, f.calls = kind, f.calls+1
	if f.onRead != nil {
		f.onRead()
	}
	return f.connections, f.err
}

func TestProcessSnapshotFields(t *testing.T) {
	process := sampleProcess()
	now := func() time.Time { return time.UnixMilli(1700000001000) }
	source := newProcessSource(&fakeProcesses{entries: []processEntry{{pid: 42, reader: process}}}, now, nil)
	got, err := source.Snapshot(context.Background())
	want := []Process{{PID: 42, ParentPID: 1, Name: "demo.exe", Cmdline: "demo.exe --serve", Exe: `C:\demo\demo.exe`, CreatedAt: 1700000000000, RSS: 4096, Threads: 3}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Snapshot() = %+v, %v; want %+v", got, err, want)
	}
}

func TestUnavailableProcessProperties(t *testing.T) {
	for _, field := range []string{"parentPid", "name", "cmdline", "exe", "cpu", "rss", "threads"} {
		t.Run(field, func(t *testing.T) {
			process := sampleProcess()
			process.failures[field] = errors.New("fake sensitive property error")
			var logs bytes.Buffer
			source := newProcessSource(&fakeProcesses{entries: []processEntry{{pid: 42, reader: process}}}, func() time.Time { return time.Unix(1, 0) }, slog.New(slog.NewTextHandler(&logs, nil)))
			got, err := source.Snapshot(context.Background())
			if err != nil || len(got) != 1 {
				t.Fatalf("one unavailable property aborted the snapshot: %+v, %v", got, err)
			}
			row := got[0]
			values := map[string]any{"parentPid": row.ParentPID, "name": row.Name, "cmdline": row.Cmdline, "exe": row.Exe, "cpu": row.CPUPercent, "rss": row.RSS, "threads": row.Threads}
			if !reflect.ValueOf(values[field]).IsZero() {
				t.Fatalf("unavailable %s was not cleared: %v", field, values[field])
			}
			if strings.Contains(logs.String(), "fake sensitive") || strings.Contains(logs.String(), process.cmdline) {
				t.Fatal("property contents were logged")
			}
		})
	}
}

func TestProcessIdentityAndCPUHistory(t *testing.T) {
	process := sampleProcess()
	clock := time.UnixMilli(1700000001000)
	backend := &fakeProcesses{entries: []processEntry{{pid: 42, reader: process}}}
	source := newProcessSource(backend, func() time.Time { return clock }, nil)
	snapshot := func() Process {
		t.Helper()
		rows, err := source.Snapshot(context.Background())
		if err != nil || len(rows) != 1 {
			t.Fatalf("Snapshot() = %+v, %v", rows, err)
		}
		return rows[0]
	}
	first := snapshot()
	clock = clock.Add(2 * time.Second)
	process.times.User += 1.25
	second := snapshot()
	if second.Identity() != first.Identity() || math.Abs(second.CPUPercent-62.5) > 1e-9 {
		t.Fatalf("CPU interval or identity is wrong: %+v", second)
	}
	clock = clock.Add(time.Second)
	process.createdAt++
	process.times.User = 1000
	reused := snapshot()
	if reused.Identity() == first.Identity() || reused.CPUPercent != 0 || len(source.previous) != 1 {
		t.Fatalf("PID reuse retained the old process identity or CPU sample: %+v", reused)
	}
	backend.entries = nil
	if rows, err := source.Snapshot(context.Background()); err != nil || len(rows) != 0 || len(source.previous) != 0 {
		t.Fatalf("exited processes were retained: %+v, %v", rows, err)
	}
}

func TestCPUSamplingHandlesGapsAndCounterReset(t *testing.T) {
	process := sampleProcess()
	clock := time.Unix(1, 0)
	source := newProcessSource(&fakeProcesses{entries: []processEntry{{pid: 42, reader: process}}}, func() time.Time { return clock }, nil)
	snapshot := func() float64 {
		t.Helper()
		rows, err := source.Snapshot(context.Background())
		if err != nil || len(rows) != 1 {
			t.Fatalf("Snapshot() = %+v, %v", rows, err)
		}
		return rows[0].CPUPercent
	}
	_ = snapshot()
	clock = clock.Add(time.Second)
	process.failures["cpu"] = errors.New("unavailable")
	if got := snapshot(); got != 0 {
		t.Fatalf("unavailable CPU = %v", got)
	}
	delete(process.failures, "cpu")
	clock = clock.Add(time.Second)
	process.times.User++
	if got := snapshot(); math.Abs(got-50) > 1e-9 {
		t.Fatalf("CPU after gap = %v, want 50", got)
	}
	clock = clock.Add(time.Second)
	process.times.User = 0
	if got := snapshot(); got != 0 {
		t.Fatalf("negative CPU delta = %v", got)
	}
	process.times.User++
	if got := snapshot(); got != 0 {
		t.Fatalf("zero elapsed CPU = %v", got)
	}
}

func TestMissingCreationTimeIsNotAnIdentity(t *testing.T) {
	for _, createdAt := range []int64{0, -1} {
		process := sampleProcess()
		process.createdAt = createdAt
		backend := &fakeProcesses{entries: []processEntry{{pid: 42, reader: process}, {pid: 43, reader: sampleProcess()}}}
		rows, err := newProcessSource(backend, func() time.Time { return time.Unix(1, 0) }, nil).Snapshot(context.Background())
		if err != nil || len(rows) != 1 || rows[0].PID != 43 {
			t.Fatalf("unknown identity was published: %+v, %v", rows, err)
		}
	}
	process := sampleProcess()
	process.failures["createdAt"] = errors.New("process exited")
	rows, err := newProcessSource(&fakeProcesses{entries: []processEntry{{pid: 42, reader: process}}}, func() time.Time { return time.Unix(1, 0) }, nil).Snapshot(context.Background())
	if err != nil || len(rows) != 0 {
		t.Fatalf("failed creation-time lookup was published: %+v, %v", rows, err)
	}
}

func TestTCPListenersOnly(t *testing.T) {
	backend := &fakeConnections{connections: []gnet.ConnectionStat{
		{Status: "ESTABLISHED", Laddr: gnet.Addr{IP: "127.0.0.1", Port: 4000}, Pid: 99},
		{Status: "LISTEN", Laddr: gnet.Addr{IP: "::", Port: 7443}, Pid: 42},
		{Status: "NONE", Laddr: gnet.Addr{IP: "0.0.0.0", Port: 53}, Pid: 1},
		{Status: "LISTEN", Laddr: gnet.Addr{IP: "0.0.0.0", Port: 8080}, Pid: 43},
		{Status: "TIME_WAIT", Laddr: gnet.Addr{IP: "127.0.0.1", Port: 3000}},
	}}
	rows, err := newPortSource(backend, func() time.Time { return time.Unix(1, 0) }, nil).ListeningTCP(context.Background())
	want := []Port{{LocalAddress: "0.0.0.0", Port: 8080, PID: 43}, {LocalAddress: "::", Port: 7443, PID: 42}}
	if err != nil || !reflect.DeepEqual(rows, want) || backend.kind != "tcp" {
		t.Fatalf("ListeningTCP() = %+v, %v, kind %q; want %+v", rows, err, backend.kind, want)
	}
}

func TestEnumerationErrorsAndCancellation(t *testing.T) {
	failure := errors.New("fake enumeration error")
	now := func() time.Time { return time.Unix(1, 0) }
	if rows, err := newProcessSource(&fakeProcesses{err: failure}, now, nil).Snapshot(context.Background()); rows != nil || !errors.Is(err, failure) {
		t.Fatalf("process error was lost: %+v, %v", rows, err)
	}
	if rows, err := newPortSource(&fakeConnections{err: failure}, now, nil).ListeningTCP(context.Background()); rows != nil || !errors.Is(err, failure) {
		t.Fatalf("port error was lost: %+v, %v", rows, err)
	}
	for _, cancelDuringRead := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		processes := &fakeProcesses{}
		if cancelDuringRead {
			processes.onRead = cancel
		} else {
			cancel()
		}
		if rows, err := newProcessSource(processes, now, nil).Snapshot(ctx); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("process cancellation was lost: %+v, %v", rows, err)
		}
		cancel()
		ctx, cancel = context.WithCancel(context.Background())
		connections := &fakeConnections{}
		if cancelDuringRead {
			connections.onRead = cancel
		} else {
			cancel()
		}
		if rows, err := newPortSource(connections, now, nil).ListeningTCP(ctx); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("port cancellation was lost: %+v, %v", rows, err)
		}
		if !cancelDuringRead && (connections.calls != 0 || processes.calls != 0) {
			t.Fatal("cancelled collection called a system backend")
		}
		if cancelDuringRead && (connections.calls != 1 || processes.calls != 1) {
			t.Fatal("cancellation-during-read fixture did not reach its backend")
		}
		cancel()
	}
}

func TestSlowCollectionWarning(t *testing.T) {
	for _, kind := range []string{"process", "port"} {
		for _, duration := range []time.Duration{time.Second, time.Second + time.Nanosecond, 2 * time.Second} {
			t.Run(kind+"/"+duration.String(), func(t *testing.T) {
				clock := time.Unix(1, 0)
				advance := func() { clock = clock.Add(duration) }
				var logs bytes.Buffer
				logger := slog.New(slog.NewTextHandler(&logs, nil))
				now := func() time.Time { return clock }
				if kind == "process" {
					_, _ = newProcessSource(&fakeProcesses{onRead: advance}, now, logger).Snapshot(context.Background())
				} else {
					_, _ = newPortSource(&fakeConnections{onRead: advance}, now, logger).ListeningTCP(context.Background())
				}
				if got, want := strings.Contains(logs.String(), "level=WARN"), duration > time.Second; got != want {
					t.Fatalf("warning = %v, want %v: %s", got, want, logs.String())
				}
				if duration > time.Second && !strings.Contains(logs.String(), "source="+kind) {
					t.Fatalf("warning does not identify its source: %s", logs.String())
				}
			})
		}
	}
}
