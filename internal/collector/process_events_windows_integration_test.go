//go:build integration && windows

package collector

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestNativeProcessEventsHelper(t *testing.T) {
	if os.Getenv("LGMON_PROCESS_EVENT_HELPER") != "1" {
		return
	}
	fmt.Fprintf(os.Stdout, "ready %d\n", os.Getpid())
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestNativeProcessEventsObserveOwnChildAndRelease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, executable, "-test.run=^TestNativeProcessEventsHelper$")
	child.Env = append(os.Environ(), "LGMON_PROCESS_EVENT_HELPER=1")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	childDone := false
	defer func() {
		input.Close()
		if !childDone {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	line, err := bufio.NewReader(output).ReadString('\n')
	var actualPID int
	_, parsed := fmt.Sscanf(line, "ready %d\n", &actualPID)
	if err != nil || parsed != nil || actualPID != child.Process.Pid {
		t.Fatal("own child was not ready", line, err)
	}
	ready := make(chan struct{})
	var session string
	var once sync.Once
	events := make(chan ProcessEvent, 256)
	done := make(chan error, 1)
	go func() {
		done <- NewProcessEventSource(time.Now).Run(ctx, ProcessEventFilter{Names: []string{filepath.Base(executable)}, PIDs: []int32{int32(child.Process.Pid)}}, func(event ProcessEvent) {
			if event.Kind == ProcessEventsReady {
				session = event.Session
				once.Do(func() { close(ready) })
				return
			}
			select {
			case events <- event:
			default:
			}
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				t.Error("native event source failed", err)
			}
			if !strings.HasPrefix(session, "lgmon-process-") {
				t.Error("source did not identify its own session")
				return
			}
			wide, _ := windows.UTF16FromString(session)
			buffer := make([]byte, 120+2*len(wide))
			properties := (*etwTraceProperties)(unsafe.Pointer(&buffer[0]))
			properties.Wnode.BufferSize = uint32(len(buffer))
			status, _, _ := etwControl.NewProc("ControlTraceW").Call(0, uintptr(unsafe.Pointer(&wide[0])), uintptr(unsafe.Pointer(properties)), 0)
			if status != 4201 {
				t.Errorf("own ETW session was not released: status=%d", status)
			}
		case <-time.After(3 * time.Second):
			t.Error("native event source did not release its temporary subscription")
		}
	}()
	select {
	case <-ready:
	case err := <-done:
		done <- err
		t.Fatal("native event subscription failed", err)
	case <-ctx.Done():
		t.Fatal("native event source did not become ready", ctx.Err())
	}
	// An idle subscription must survive the finite NextEvent timeout before any test process exists.
	select {
	case err := <-done:
		done <- err
		t.Fatal("idle event subscription failed", err)
	case <-time.After(450 * time.Millisecond):
	}
	exitedAt := time.Now()
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	childDone = true
	t.Logf("own process finished after %s", time.Since(exitedAt))
	exitDeadline := time.NewTimer(time.Second - time.Since(exitedAt))
	defer exitDeadline.Stop()
	for {
		select {
		case event := <-events:
			if event.PID == int32(child.Process.Pid) && event.Kind == ProcessExited {
				elapsed := time.Since(exitedAt)
				if elapsed >= time.Second || event.Name == "" || event.TS <= 0 {
					t.Fatal("invalid or delayed own process exit", elapsed, event)
				}
				t.Logf("own process exit observed after %s, name=%q", elapsed, event.Name)
				return
			}
		case <-exitDeadline.C:
			t.Fatal("own process exit was not observed within one second")
		case err := <-done:
			done <- err
			t.Fatal("event source failed", err)
		}
	}
}
