//go:build integration && windows

package collector

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessSourceFindsOwnPing(t *testing.T) {
	command := exec.Command("ping", "-t", "127.0.0.1")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("clean up own ping process: %v", err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("own ping process did not exit")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var logs bytes.Buffer
	defer func() {
		if logs.Len() > 0 {
			t.Log(logs.String())
		}
	}()
	source := NewProcessSource(time.Now, slog.New(slog.NewTextHandler(&logs, nil)))
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		rows, err := source.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.PID != int32(command.Process.Pid) {
				continue
			}
			if !strings.EqualFold(row.Name, "ping.exe") || row.CreatedAt <= 0 || row.ParentPID != int32(os.Getpid()) {
				t.Fatalf("owned ping identity is incorrect: pid=%d parent=%d name=%q createdAt=%d", row.PID, row.ParentPID, row.Name, row.CreatedAt)
			}
			if row.Cmdline != "" && !strings.Contains(row.Cmdline, "127.0.0.1") {
				t.Fatalf("owned ping command line is incorrect: %q", row.Cmdline)
			}
			if row.Exe != "" && !strings.EqualFold(filepath.Base(row.Exe), "ping.exe") {
				t.Fatalf("owned ping executable is incorrect: %q", row.Exe)
			}
			if row.RSS == 0 || row.Threads <= 0 {
				t.Fatalf("owned ping metrics are missing: rss=%d threads=%d", row.RSS, row.Threads)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("owned ping was not found")
		case <-ticker.C:
		}
	}
}

func TestPortSourceFindsOwnListener(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	address := listener.Addr().(*net.TCPAddr)
	source := NewPortSource(time.Now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := source.ListeningTCP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Port == uint32(address.Port) && row.LocalAddress == "127.0.0.1" && row.PID == int32(os.Getpid()) {
			return
		}
	}
	t.Fatal("owned TCP listener and PID were not found")
}
