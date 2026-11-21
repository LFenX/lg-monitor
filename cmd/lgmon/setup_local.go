// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/winsvc"
	"github.com/shirou/gopsutil/v4/process"
)

// setupLocalCopy is a running lgmon program: its image and its arguments after the program name.
type setupLocalCopy struct {
	PID        int32
	Executable string
	Args       []string
}

// selfRunHome is the home a copy runs lgmon's monitor for, and whether it runs one at all: opening lgmon with no
// command (which runs it with a tray when no service answers) or `lgmon run`. Agents' `lgmon mcp` sessions, the
// installer's own `lgmon setup` and every other command are not monitors; neither is anything with a flag lgmon
// does not take before its command.
func selfRunHome(args []string) (string, bool) {
	home, command := "", ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--home" || arg == "--log-level":
			if index+1 < len(args) {
				if arg == "--home" {
					home = args[index+1]
				}
				index++
			}
		case strings.HasPrefix(arg, "--home="):
			home = strings.TrimPrefix(arg, "--home=")
		case strings.HasPrefix(arg, "-"):
			known := arg == "--json" || strings.HasPrefix(arg, "--json=") || strings.HasPrefix(arg, "--log-level=")
			if command == "" && !known {
				return "", false
			}
		case command == "":
			if command = arg; command != "run" {
				return "", false
			}
		}
	}
	return home, true
}

// stopSelfRunCopies ends the copies in programDirectory that run the monitor for home. A copy naming no home runs
// the one the setup.ini beside it records.
func stopSelfRunCopies(ctx context.Context, programDirectory, home string, copies []setupLocalCopy, stop func(context.Context, int32) error) (int, error) {
	recorded, _ := winsvc.ReadSetupMetadata(programDirectory)
	stopped := 0
	for _, running := range copies {
		image := strings.ToLower(filepath.Base(running.Executable))
		if !setupSamePath(filepath.Dir(running.Executable), programDirectory) || (image != "lgmon.exe" && !strings.HasPrefix(image, "lgmon.exe.previous-")) {
			continue
		}
		runs, monitor := selfRunHome(running.Args)
		if runs == "" {
			runs = recorded.Home
		}
		if !monitor || runs == "" || !setupSamePath(runs, home) {
			continue
		}
		if err := stop(ctx, running.PID); err != nil {
			return stopped, fmt.Errorf("结束在服务之外运行、占用这个数据目录的 lgmon（进程 %d）失败，请先在托盘里退出它再重试：%w", running.PID, err)
		}
		stopped++
	}
	return stopped, nil
}

func nativeLocalCopies(ctx context.Context) ([]setupLocalCopy, error) {
	processes, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("列出正在运行的程序失败：%w", err)
	}
	var copies []setupLocalCopy
	for _, item := range processes {
		if name, err := item.NameWithContext(ctx); err != nil || !strings.HasPrefix(strings.ToLower(name), "lgmon.exe") {
			continue
		}
		executable, err := item.ExeWithContext(ctx)
		if err != nil || executable == "" {
			continue
		}
		args, err := item.CmdlineSliceWithContext(ctx)
		if err != nil || len(args) == 0 {
			continue
		}
		copies = append(copies, setupLocalCopy{PID: item.Pid, Executable: executable, Args: args[1:]})
	}
	return copies, nil
}

// nativeStopProcess ends pid and waits until it is gone, so its ports and database are free for the service.
func nativeStopProcess(ctx context.Context, pid int32) error {
	item, err := process.NewProcessWithContext(ctx, pid)
	if err != nil {
		return nil
	}
	if err := item.KillWithContext(ctx); err != nil {
		if running, _ := item.IsRunningWithContext(ctx); running {
			return err
		}
		return nil
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if running, err := item.IsRunningWithContext(ctx); err == nil && !running {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("进程 %d 在 10 秒内没有退出", pid)
		case <-tick.C:
		}
	}
}

func nativeStopLocal(program string) func(context.Context, string) (int, error) {
	return func(ctx context.Context, home string) (int, error) {
		copies, err := nativeLocalCopies(ctx)
		if err != nil {
			return 0, err
		}
		return stopSelfRunCopies(ctx, filepath.Dir(program), home, copies, nativeStopProcess)
	}
}
