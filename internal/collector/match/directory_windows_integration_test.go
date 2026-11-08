//go:build windows && integration

package match

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	gprocess "github.com/shirou/gopsutil/v4/process"
)

func TestNativeWorkingDirectoriesSeparateOwnedPingProcesses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	exe := filepath.Join(os.Getenv("WINDIR"), "System32", "ping.exe")
	var owned []*exec.Cmd
	for _, name := range []string{"甲's [A]", "乙's [B]"} {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(exe, "-t", "127.0.0.1")
		command.Dir, command.SysProcAttr = dir, &syscall.SysProcAttr{HideWindow: true}
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = command.Process.Kill()
			_ = command.Wait()
		})
		owned = append(owned, command)
		process := &gprocess.Process{Pid: int32(command.Process.Pid)}
		for {
			cwd, err := process.CwdWithContext(ctx)
			if err == nil && strings.EqualFold(strings.TrimSuffix(cwd, `\`), dir) {
				break
			}
			if ctx.Err() != nil {
				t.Fatalf("owned process directory not ready: %q %v", cwd, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	source := collector.NewProcessSource(time.Now, nil)
	rows, err := source.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var processes []collector.Process
	for _, row := range rows {
		if int(row.PID) == owned[0].Process.Pid || int(row.PID) == owned[1].Process.Pid {
			processes = append(processes, row)
		}
	}
	if len(processes) != 2 {
		t.Fatalf("owned processes missing: %+v", processes)
	}
	directories := source.(collector.WorkingDirectorySource)
	for i, command := range owned {
		matcher, err := New([]config.Target{{ID: fmt.Sprintf("owned-%d", i), Match: config.Match{Process: "ping.exe", WorkDir: command.Dir}}})
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := matcher.ResolveDirectories(ctx, collector.ProcessSnapshot{Processes: processes}, directories)
		if err != nil {
			t.Fatal(err)
		}
		result := matcher.Match(resolved, nil)[0]
		if result.State != "up" || len(result.Processes) != 1 || int(result.Processes[0].PID) != command.Process.Pid {
			t.Fatalf("working directories cross-matched: %+v", result)
		}
	}
	matcher, err := New([]config.Target{{ID: "system-ping", Match: config.Match{Process: "ping.exe", ExeDir: filepath.Dir(exe)}}})
	if err != nil {
		t.Fatal(err)
	}
	if result := matcher.Match(processes, nil)[0]; result.State != "up" || len(result.Processes) != 2 {
		t.Fatalf("System32 executable directory not matched: %+v", result)
	}
}
