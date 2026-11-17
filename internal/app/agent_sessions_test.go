package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
)

func agentSessionsFixture() (*APIBackend, *time.Time) {
	now := time.UnixMilli(1_800_000_000_000)
	b := &APIBackend{options: APIBackendOptions{Now: func() time.Time { return now }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	return b, &now
}

func presence(t *testing.T, b *APIBackend, agent api.AgentClient, body string) {
	t.Helper()
	if _, err := b.agentPresence(context.Background(), api.Request{Via: "mcp", Agent: agent, Body: []byte(body)}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentsShowEveryConnectedAgentByItsOwnName(t *testing.T) {
	b, now := agentSessionsFixture()
	claude := api.AgentClient{Kind: "claude-code", Client: "claude-code", Version: "2.1.0", Session: "stdio-a"}
	codex := api.AgentClient{Client: "codex-mcp-client", Session: "stdio-b"}
	presence(t, b, claude, `{"state":"open","interval":30}`)
	*now = now.Add(time.Second)
	presence(t, b, codex, `{"state":"open","interval":30}`)
	b.noteAgent(context.Background(), api.Request{Via: "mcp", Agent: claude})
	// An older lgmon mcp says nothing about its agent: it is one connection per token, named by the token.
	b.noteAgent(context.Background(), api.Request{Via: "mcp"})
	// The page and 波西米亚 are not agents.
	b.noteAgent(context.Background(), api.Request{Via: "assistant", Agent: api.AgentClient{Session: "x"}})
	b.agentPresence(context.Background(), api.Request{Agent: api.AgentClient{Session: "page"}, Body: []byte(`{}`)})

	list := b.listAgents()
	if len(list) != 3 {
		t.Fatalf("list=%+v", list)
	}
	byName := map[string]api.AgentSession{}
	for _, item := range list {
		byName[item.Name] = item
		if !item.Connected || item.ID == "" {
			t.Fatalf("not connected: %+v", item)
		}
	}
	if got := byName["Claude Code"]; got.Kind != "claude-code" || got.Calls != 1 || got.Version != "2.1.0" {
		t.Fatalf("claude=%+v", got)
	}
	if got := byName["Codex"]; got.Kind != "codex" || got.Calls != 0 || got.Client != "codex-mcp-client" {
		t.Fatalf("codex=%+v", got)
	}
	if got := byName[""]; got.Kind != "" || got.Calls != 1 {
		t.Fatalf("legacy=%+v", got)
	}
	if b.agent.seenName != "" || b.agent.seenAt != now.UnixMilli() {
		t.Fatalf("seen=%q at %d", b.agent.seenName, b.agent.seenAt)
	}

	// Codex says goodbye; Claude Code misses its heartbeats; the legacy one stays within its quiet window.
	presence(t, b, codex, `{"state":"closed","interval":30}`)
	*now = now.Add(2 * time.Minute)
	list = b.listAgents()
	if len(list) != 3 || list[0].Name != "" || !list[0].Connected || list[1].Connected || list[2].Connected {
		t.Fatalf("after two minutes=%+v", list)
	}
	// Disconnected agents stay for a day, then go.
	*now = now.Add(agentKeep)
	if list := b.listAgents(); len(list) != 0 {
		t.Fatalf("after a day=%+v", list)
	}

	// Coming back after closing starts a new connection.
	presence(t, b, codex, `{"state":"open","interval":30}`)
	if list := b.listAgents(); len(list) != 1 || list[0].Since != now.UnixMilli() || !list[0].Connected {
		t.Fatalf("reconnected=%+v", list)
	}
}

func TestAgentPresenceRejectsBadReportsAndBoundsTheList(t *testing.T) {
	b, now := agentSessionsFixture()
	for _, body := range []string{`{"state":"gone"}`, `{"interval":-1}`, `{"interval":601}`, `[]`} {
		if _, err := b.agentPresence(context.Background(), api.Request{Via: "mcp", Body: []byte(body)}); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	for i := range agentSessionLimit + 10 {
		*now = now.Add(time.Second)
		presence(t, b, api.AgentClient{Session: fmt.Sprint("stdio-", i)}, `{"state":"open","interval":30}`)
	}
	list := b.listAgents()
	if len(list) != agentSessionLimit || list[0].LastSeenAt != now.UnixMilli() {
		t.Fatalf("kept %d, newest %+v", len(list), list[0])
	}
}
