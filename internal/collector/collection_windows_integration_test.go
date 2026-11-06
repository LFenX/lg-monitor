//go:build integration && windows

package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"golang.org/x/sys/windows"
)

func TestNativeAdditionalCollectionIsReadOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	metrics, err := NewMetricsSource(time.Now, nil).Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := metrics.Validate(); err != nil {
		t.Fatal(err)
	}
	if metrics.MemoryTotal == 0 || metrics.UptimeSeconds == 0 || len(metrics.Disks) == 0 || len(metrics.Networks) == 0 {
		t.Fatalf("native metrics incomplete: %+v", metrics)
	}
	source := NewServiceSource(time.Now, nil)
	services, err := source.Snapshot(ctx)
	if err != nil || len(services) == 0 {
		t.Fatalf("native service enumeration: %d %v", len(services), err)
	}
	for _, service := range services {
		if err := service.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	selected, err := source.Lookup(ctx, []string{services[0].Name})
	if err != nil || len(selected) != 1 || !strings.EqualFold(selected[0].Name, services[0].Name) {
		t.Fatalf("native service lookup: %+v %v", selected, err)
	}
	t.Logf("read-only collection: memory=%d disks=%d networkInterfaces=%d services=%d", metrics.MemoryTotal, len(metrics.Disks), len(metrics.Networks), len(services))
}

func scriptHelperCommand(t *testing.T, mode, path string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return windows.ComposeCommandLine([]string{executable, "-test.run=^TestNativeScriptHelper$", "--", mode, path})
}

func TestNativeScriptProbeBranches(t *testing.T) {
	t.Setenv("LGMON_SCRIPT_HELPER", "1")
	prober := NewProber(time.Now)
	defer prober.Close()
	for _, tc := range []struct {
		mode     string
		ok       bool
		contains string
	}{
		{"success", true, ""}, {"false", false, "检查失败"}, {"exit", false, "退出码为 7"}, {"invalid", false, "不是约定格式"}, {"wait", false, "超时"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			timeout := "30s"
			if tc.mode == "wait" {
				timeout = "300ms"
			}
			result := prober.Execute(context.Background(), "fake-script", 0, config.Probe{Type: "script", Run: scriptHelperCommand(t, tc.mode, "unused"), Timeout: config.Duration(timeout)})
			if result.OK != tc.ok || !strings.Contains(result.Error, tc.contains) {
				t.Fatalf("native script %s: %+v", tc.mode, result)
			}
			if tc.ok && result.Metrics["age"] != 2 {
				t.Fatalf("native script metrics lost: %+v", result)
			}
		})
	}
}

func TestNativeScriptCancellationCleansOwnDescendants(t *testing.T) {
	t.Setenv("LGMON_SCRIPT_HELPER", "1")
	path := filepath.Join(t.TempDir(), "own-pids.json")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	command := scriptHelperCommand(t, "parent", path)
	go func() { _, err := (nativeScriptRunner{}).Run(ctx, command); done <- err }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("script runner did not clean up")
		}
	}()
	var pids []uint32
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for len(pids) != 2 {
		data, _ := os.ReadFile(path)
		_ = json.Unmarshal(data, &pids)
		if len(pids) == 2 {
			break
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("script exited before creating its own child: %v", err)
		case <-ctx.Done():
			t.Fatal("script helper did not become ready")
		case <-ticker.C:
		}
	}
	var handles []windows.Handle
	for _, pid := range pids {
		handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, handle)
		defer windows.CloseHandle(handle)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("canceled script succeeded")
		}
		done <- err
	case <-time.After(5 * time.Second):
		t.Fatal("canceled script did not return")
	}
	for _, handle := range handles {
		status, err := windows.WaitForSingleObject(handle, 5000)
		if err != nil || status != windows.WAIT_OBJECT_0 {
			t.Fatalf("own script descendant still running: status=%d err=%v", status, err)
		}
	}
}

func TestNativeScriptHelper(t *testing.T) {
	if os.Getenv("LGMON_SCRIPT_HELPER") != "1" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+2 >= len(os.Args) {
		os.Exit(9)
	}
	mode, path := os.Args[separator+1], os.Args[separator+2]
	switch mode {
	case "success":
		fmt.Println("progress")
		fmt.Println(`{"ok":true,"message":"ready","metrics":{"age":2}}`)
	case "false":
		fmt.Println(`{"ok":false,"message":"old backup"}`)
	case "exit":
		fmt.Println("own failure output")
		os.Exit(7)
	case "invalid":
		fmt.Println("not JSON")
	case "parent":
		executable, err := os.Executable()
		if err != nil {
			os.Exit(9)
		}
		child := exec.Command(executable, "-test.run=^TestNativeScriptHelper$", "--", "wait", "unused")
		child.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := child.Start(); err != nil {
			os.Exit(9)
		}
		data, _ := json.Marshal([]uint32{uint32(os.Getpid()), uint32(child.Process.Pid)})
		if os.WriteFile(path, data, 0600) != nil {
			_ = child.Process.Kill()
			_ = child.Wait()
			os.Exit(9)
		}
		_ = child.Wait()
	case "wait":
		fmt.Println("waiting")
		time.Sleep(time.Minute)
	default:
		os.Exit(9)
	}
	os.Exit(0)
}
