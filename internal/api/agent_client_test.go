// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func mcpPost(s *Server, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("POST", "http://127.0.0.1:7444/mcp", strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:4321"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer fake-read")
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	return response
}

func TestHTTPMCPRemembersTheAgentFromInitializeForLaterCalls(t *testing.T) {
	var mu sync.Mutex
	seen := map[Operation][]AgentClient{}
	server := serverFixture(t, func(opts *ServerOptions) {
		for _, op := range []Operation{AgentPresence, ListCollectors} {
			opts.Actions[op] = func(_ context.Context, r Request) (any, error) {
				mu.Lock()
				defer mu.Unlock()
				if r.Via != "mcp" {
					t.Errorf("%s via %q", op, r.Via)
				}
				seen[op] = append(seen[op], r.Agent)
				if op == ListCollectors {
					return []Collector{}, nil
				}
				return Confirmation{true, "已记录"}, nil
			}
		}
	})
	response := mcpPost(server, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude-code","version":"2.1.0"}}}`)
	if response.Code != 200 {
		t.Fatalf("initialize status=%d body=%s", response.Code, response.Body.String())
	}
	response = mcpPost(server, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_collectors","arguments":{}}}`)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"result"`) {
		t.Fatalf("call status=%d body=%s", response.Code, response.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	presence, calls := seen[AgentPresence], seen[ListCollectors]
	if len(presence) != 1 || len(calls) != 1 {
		t.Fatalf("seen=%+v", seen)
	}
	want := AgentClient{Kind: "claude-code", Client: "claude-code", Version: "2.1.0", Session: httpAgentSession("fake-read", "claude-code")}
	if presence[0] != want || calls[0] != want {
		t.Fatalf("presence=%+v call=%+v want=%+v", presence[0], calls[0], want)
	}
}

func TestMCPClientInfoReadsBothProtocolsAndKeepsTheBody(t *testing.T) {
	for _, test := range []struct {
		body           string
		want           AgentClient
		opening, found bool
		agent          string
	}{
		{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"codex-mcp-client","title":"Codex","version":"0.9"}}}`, AgentClient{Kind: "codex", Client: "codex-mcp-client", Version: "0.9"}, true, true, ""},
		{`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientInfo":{"name":"opencode","version":"1.2"}}}}`, AgentClient{Kind: "opencode", Client: "opencode", Version: "1.2"}, true, true, ""},
		{`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_collectors","_meta":{"io.modelcontextprotocol/clientInfo":{"name":"mcp","title":"Kimi Code"}}}}`, AgentClient{Kind: "kimi", Client: "mcp"}, false, true, ""},
		{`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_collectors"}}`, AgentClient{}, false, false, ""},
		// A generic client name falls back to the User-Agent.
		{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"mcp-client"}}}`, AgentClient{Kind: "zcode", Client: "mcp-client"}, true, true, "ZCode/3.1"},
	} {
		request := httptest.NewRequest("POST", "http://127.0.0.1:7444/mcp", strings.NewReader(test.body))
		if test.agent != "" {
			request.Header.Set("User-Agent", test.agent)
		}
		client, opening, found := mcpClientInfo(request)
		if client != test.want || opening != test.opening || found != test.found {
			t.Errorf("%s: got %+v opening=%t found=%t", test.body, client, opening, found)
		}
		if rest, err := io.ReadAll(request.Body); err != nil || string(rest) != test.body {
			t.Errorf("body after peek=%q %v", rest, err)
		}
	}
}

func TestAgentHeadersAreLabelsOnly(t *testing.T) {
	header := http.Header{}
	setAgentHeader(header, AgentClient{Kind: "codex", Client: "名字\r\nX-Evil: 1 codex", Version: strings.Repeat("9", 200), Session: "stdio-1"})
	got := agentFromHeader(header)
	if got.Kind != "codex" || got.Client != "X-Evil: 1 codex" || len(got.Version) != agentHeaderMaxBytes || got.Session != "stdio-1" {
		t.Fatalf("got %+v", got)
	}
	setAgentHeader(header, AgentClient{Session: "stdio-2"})
	if header.Get(headerAgentKind) != "" || header.Get(headerAgentSession) != "stdio-2" {
		t.Fatalf("stale header %v", header)
	}
}
