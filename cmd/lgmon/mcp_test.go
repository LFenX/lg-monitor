// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/agents"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPCommandRunsRealStdioAndUsesOnlyHTTPToken(t *testing.T) {
	var calls int
	var mu sync.Mutex
	var reports, sessions []string
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		// The client name is unknown, so the agent is named from the process that started lgmon mcp.
		if request.Header.Get("X-Lgmon-Agent") != "kimi" || request.Header.Get("X-Lgmon-Client") != "stdio-test" || request.Header.Get("X-Lgmon-Client-Version") != "fake" {
			t.Errorf("agent headers=%v", request.Header)
		}
		sessions = append(sessions, request.Header.Get("X-Lgmon-Session"))
		if request.URL.Path == "/api/v1/agents/presence" && request.Method == "POST" {
			body, _ := io.ReadAll(request.Body)
			reports = append(reports, string(body))
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"ok":true,"message":"已记录"}`)
			return
		}
		if request.URL.Path != "/api/v1/collectors" || request.Header.Get("Authorization") != "Bearer fake-read" || request.Method != "GET" {
			t.Errorf("unexpected proxy request: method=%s path=%s", request.Method, request.URL.Path)
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `[{"id":"fake-collector","hostname":"虚构主机"}]`)
	}))
	defer apiServer.Close()
	input, clientInput := io.Pipe()
	clientOutput, output := io.Pipe()
	defer input.Close()
	defer clientInput.Close()
	defer clientOutput.Close()
	defer output.Close()
	command := newMCPCommand(runMCP, func() string { return "fake-read" }, func(context.Context) []agents.Process {
		return []agents.Process{{Executable: `C:\Python\python.exe`, CommandLine: "python -m kimi_cli"}}
	})
	command.SetArgs([]string{"--url", apiServer.URL})
	command.SetIn(input)
	command.SetOut(output)
	var stderr bytes.Buffer
	command.SetErr(&stderr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- command.ExecuteContext(ctx)
		output.Close()
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio-test", Version: "fake"}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: clientOutput, Writer: clientInput}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 107 {
		t.Fatalf("tools=%d %v", len(tools.Tools), err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_collectors", Arguments: map[string]any{}})
	mu.Lock()
	defer mu.Unlock()
	if err != nil || result.IsError || calls != 1 || len(result.Content) == 0 || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "fake-collector") {
		t.Fatalf("result=%+v calls=%d %v", result, calls, err)
	}
	mu.Unlock()
	session.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("stdio command did not exit after disconnect")
	}
	mu.Lock()
	if strings.Join(reports, ",") != `{"state":"open","interval":30},{"state":"closed","interval":30}` {
		t.Fatalf("reports=%v", reports)
	}
	for _, id := range sessions {
		if !strings.HasPrefix(id, "stdio-") || id != sessions[0] {
			t.Fatalf("sessions=%v", sessions)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr=%q", stderr.String())
	}
}

func TestMCPCommandRejectsMissingTokenAndUnsafeURLsBeforeStarting(t *testing.T) {
	for _, address := range []string{"http://127.0.0.1:7444", "file:///fake", "http://user:password@example.test", "http://example.test/?token=fake", "http://example.test/api", "http://example.test/#fragment"} {
		command := newMCPCommand(func(context.Context, *mcp.Server, mcp.Transport) error {
			t.Fatal("invalid setup started stdio")
			return errors.New("unreachable")
		}, func() string {
			if address == "http://127.0.0.1:7444" {
				return ""
			}
			return "fake-read"
		}, nil)
		command.SetArgs([]string{"--url", address})
		command.SetOut(io.Discard)
		command.SetErr(io.Discard)
		if err := command.Execute(); err == nil {
			t.Fatalf("accepted invalid setup %s", address)
		}
	}
}
