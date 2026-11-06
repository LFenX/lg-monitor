//go:build integration && windows

package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/store"
)

func TestLocalRuntimeOwnListenerAndStoppedQueries(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	data := fmt.Sprintf("targets:\n- id: own-listener\n  match: {port: %d}\n", port)
	if err := os.WriteFile(filepath.Join(home, "config", "targets.yaml"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	finished := make(chan struct{})
	var runErr error
	var notice bytes.Buffer
	go func() {
		defer close(finished)
		runErr = RunLocal(ctx, LocalOptions{Home: home, Version: "integration", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Notice: func(message string) error { _, err := fmt.Fprintln(&notice, message); return err }})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("local runtime did not release resources")
		}
	})
	path := filepath.Join(home, "data", "lgmon.db")
	waitState := func(want string) {
		t.Helper()
		started := time.Now()
		for {
			select {
			case <-finished:
				t.Fatalf("runtime exited before %s: %v", want, runErr)
			case <-ctx.Done():
				t.Fatalf("timed out waiting for %s", want)
			default:
			}
			if view, err := store.OpenView(ctx, path); err == nil {
				targets, queryErr := view.CurrentTargets(ctx)
				view.Close()
				if queryErr == nil && len(targets) == 1 && targets[0].State == want {
					t.Logf("Own listener reached %s in %s", want, time.Since(started))
					return
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitState("up")
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	waitState("down")
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("local runtime did not stop")
	}
	if runErr != nil || !strings.Contains(notice.String(), "feishu setup") {
		t.Fatalf("local runtime shutdown/notice: %v %s", runErr, notice.String())
	}
	view, err := store.OpenView(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	targets, err := view.CurrentTargets(context.Background())
	if err != nil || len(targets) != 1 || targets[0].State != "down" {
		t.Fatalf("stopped query: %+v %v", targets, err)
	}
}
