//go:build integration && windows

package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/secrets"
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

func scriptHelperHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "own home with spaces")
	scripts := filepath.Join(home, "scripts")
	if err := os.MkdirAll(scripts, 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	copy, err := os.Create(filepath.Join(scripts, "own-helper.exe"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(copy, source)
	closeErr := copy.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if err := secrets.NativePathPolicy().ProtectTree(home, false); err != nil {
		t.Fatal(err)
	}
	return home
}

func scriptHelperCommand(mode, path string) ScriptCommand {
	return ScriptCommand{Name: "own-helper.exe", Args: []string{"-test.run=TestNativeScriptHelper", "--", mode, path}}
}

func TestNativeScriptProbeBranches(t *testing.T) {
	t.Setenv("LGMON_SCRIPT_HELPER", "1")
	home := scriptHelperHome(t)
	prober := NewProberForHome(home, "fake-script-host", time.Now)
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
			command := scriptHelperCommand(tc.mode, "unused")
			result := prober.Execute(context.Background(), "fake-script", 0, config.Probe{Type: "script", Script: command.Name, Args: command.Args, Timeout: config.Duration(timeout)})
			if result.OK != tc.ok || !strings.Contains(result.Error, tc.contains) {
				t.Fatalf("native script %s: %+v", tc.mode, result)
			}
			if tc.ok && result.Metrics["age"] != 2 {
				t.Fatalf("native script metrics lost: %+v", result)
			}
		})
	}
}

func TestNativeScriptPowerShellAndBatchArguments(t *testing.T) {
	home := scriptHelperHome(t)
	for name, content := range map[string]string{
		"own check.ps1": "param([string]$Label)\n@{ok=$true;message=$Label;metrics=@{age=2}} | ConvertTo-Json -Compress\n",
		"own check.cmd": "@echo off\r\necho {\"ok\":true,\"message\":\"%~1\",\"metrics\":{\"age\":2}}\r\n",
		"own check.bat": "@echo off\r\necho {\"ok\":true,\"message\":\"%~1\",\"metrics\":{\"age\":2}}\r\n",
	} {
		if err := os.WriteFile(filepath.Join(home, "scripts", name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := secrets.NativePathPolicy().ProtectTree(home, false); err != nil {
		t.Fatal(err)
	}
	prober := NewProberForHome(home, "fake-host", time.Now)
	defer prober.Close()
	for _, name := range []string{"own check.ps1", "own check.cmd", "own check.bat"} {
		// Shared Windows runners can spend over ten seconds starting PowerShell.
		result := prober.Execute(context.Background(), "fake-script", 0, config.Probe{Type: "script", Script: name, Args: []string{"safe argument"}, Timeout: "30s"})
		if !result.OK || result.Message != "safe argument" || result.Metrics["age"] != 2 {
			t.Fatalf("script argument or interpreter: %s %+v", name, result)
		}
	}
}

func TestNativeScriptMissingAndUnsafePathsNeverExecute(t *testing.T) {
	home := scriptHelperHome(t)
	prober := NewProberForHome(home, "fake-script-host", time.Now)
	defer prober.Close()
	probe := config.Probe{Type: "script", Script: "missing.ps1", Timeout: "5s"}
	result := prober.Execute(context.Background(), "fake", 0, probe)
	if result.OK || !strings.Contains(result.Error, "采集器 fake-script-host 的 scripts 目录里没有 missing.ps1") {
		t.Fatalf("missing script diagnostic: %+v", result)
	}
	scripts := filepath.Join(home, "scripts")
	filename := filepath.Join(scripts, "own-marker.cmd")
	if err := os.WriteFile(filename, []byte("@echo off\r\necho ran>own-marker.txt\r\necho {\"ok\":true}\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := secrets.NativePathPolicy()
	probe.Script = "own-marker.cmd"
	for _, path := range []string{scripts, filename} {
		if err := policy.ProtectTree(home, false); err != nil {
			t.Fatal(err)
		}
		descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;GW;;;BU)")
		if err != nil {
			t.Fatal(err)
		}
		dacl, _, err := descriptor.DACL()
		if err != nil {
			t.Fatal(err)
		}
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
			t.Fatal(err)
		}
		result := prober.Execute(context.Background(), "fake", 0, probe)
		if result.OK || !strings.Contains(result.Error, "权限不安全") || !strings.Contains(result.Error, "fake-script-host") {
			t.Fatalf("unsafe script ran: %+v", result)
		}
		if _, err := os.Stat(filepath.Join(scripts, "own-marker.txt")); !os.IsNotExist(err) {
			t.Fatal("unsafe script created its marker")
		}
	}
}

func TestNativeBatchQuotesMetacharacterHomeAndRejectsExpansion(t *testing.T) {
	for _, directory := range []string{"own&home", "own%TEMP%home", "own!home"} {
		home := filepath.Join(t.TempDir(), directory)
		if err := os.MkdirAll(filepath.Join(home, "scripts"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "scripts", "own.cmd"), []byte("@echo off\r\necho {\"ok\":true}\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := secrets.NativePathPolicy().ProtectTree(home, false); err != nil {
			t.Fatal(err)
		}
		prober := NewProberForHome(home, "fake-host", time.Now)
		result := prober.Execute(context.Background(), "fake", 0, config.Probe{Type: "script", Script: "own.cmd", Timeout: "5s"})
		prober.Close()
		if directory == "own&home" {
			if !result.OK {
				t.Fatalf("cmd split a quoted home path: %+v", result)
			}
		} else if result.OK || !strings.Contains(result.Error, "环境变量展开字符") {
			t.Fatalf("cmd expanded home path: %+v", result)
		}
	}
}

func TestNativeScriptCancellationCleansOwnDescendants(t *testing.T) {
	t.Setenv("LGMON_SCRIPT_HELPER", "1")
	home := scriptHelperHome(t)
	path := filepath.Join(home, "scripts", "own-pids.json")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	command := scriptHelperCommand("parent", "own-pids.json")
	go func() {
		_, err := (nativeScriptRunner{home: home, hostname: "fake-host", policy: secrets.NativePathPolicy()}).Run(ctx, command)
		done <- err
	}()
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
