//go:build windows

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func TestConnectElevationPipeReturnsOnlyTheRequestedScope(t *testing.T) {
	for _, returnedScope := range []api.Scope{api.ScopeConfig, api.ScopeAdmin} {
		t.Run(string(returnedScope), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			request := connectRequest{Executable: `C:\Fake Folder\lgmon.exe`, Home: `C:\Fake Home`, URL: "http://127.0.0.1:17444", Scope: "config", Name: "fake-agent", Tools: "all"}
			childDone := make(chan error, 1)
			native := nativeConnectElevation{launch: func(executable string, args []string) error {
				if executable != request.Executable || len(args) != 4 || args[2] != "--result-pipe" || !strings.HasPrefix(args[3], connectPipePrefix) {
					t.Fatal("elevation exposed data paths or used a shell command")
				}
				go func() {
					connection, err := winio.DialPipeContext(ctx, args[3])
					if err != nil {
						childDone <- err
						return
					}
					defer connection.Close()
					var got connectRequest
					if err := json.NewDecoder(connection).Decode(&got); err != nil {
						childDone <- err
						return
					}
					if got != request {
						childDone <- context.Canceled
						return
					}
					childDone <- json.NewEncoder(connection).Encode(connectOutput{Status: "ok", Token: "FAKE_MEMORY_ONLY_TOKEN", TokenID: "fake-token", Scope: returnedScope, Home: got.Home, Executable: got.Executable, URL: got.URL, Tools: got.Tools})
				}()
				return nil
			}}
			result, err := native.Connect(ctx, request)
			if childErr := <-childDone; childErr != nil {
				t.Fatal(childErr)
			}
			if returnedScope == api.ScopeConfig {
				if err != nil || result.Token != "FAKE_MEMORY_ONLY_TOKEN" {
					t.Fatalf("pipe response failed: %v", err)
				}
			} else if err == nil || result.Token != "" {
				t.Fatal("a helper expanded config to admin")
			}
		})
	}
}

func TestConnectElevationCancellationNeverLaunchesTwice(t *testing.T) {
	calls := 0
	native := nativeConnectElevation{launch: func(string, []string) error { calls++; return windows.ERROR_CANCELLED }}
	result, err := native.Connect(context.Background(), connectRequest{})
	if err == nil || !strings.Contains(err.Error(), "已取消") || calls != 1 || result.Token != "" {
		t.Fatal("cancelled consent retried or returned credentials")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := native.Connect(ctx, connectRequest{}); err == nil || calls != 1 {
		t.Fatal("an already cancelled request launched consent")
	}
}

func TestConnectElevationHelperRejectsArbitraryPipePaths(t *testing.T) {
	for _, path := range []string{`\\other-host\pipe\foreign`, `\\.\pipe\foreign`, connectPipePrefix + strings.Repeat("z", 48)} {
		if err := serveConnectElevation(context.Background(), path, func(connectRequest) connectOutput { t.Fatal("invalid pipe reached issuance"); return connectOutput{} }); err == nil {
			t.Fatal("helper accepted a foreign pipe")
		}
	}
}
