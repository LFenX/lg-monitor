// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/agents"
	"github.com/LFenX/lg-monitor/internal/api"
)

const (
	// An agent that never reports in (HTTP MCP, older lgmon mcp) counts as connected this long after its last call.
	agentQuietWindow = 10 * time.Minute
	// Disconnected agents stay listed for a day, so the settings page can say who was here.
	agentKeep = 24 * time.Hour
	// Bounded because the session id comes from the caller.
	agentSessionLimit = 32
)

// agentSession is one MCP connection, as noteAgent and the presence heartbeats saw it.
type agentSession struct {
	api.AgentSession
	interval int
	closed   bool
}

func (s *agentSession) connected(now int64) bool {
	if s.closed {
		return false
	}
	window := agentQuietWindow.Milliseconds()
	if s.interval > 0 {
		// Two missed heartbeats and a little slack before an agent is called gone.
		window = int64(s.interval)*2500 + 5000
	}
	return now-s.LastSeenAt <= window
}

// agentIdentity names the agent behind r: the kind its MCP server recognized, else the client name, else the token.
func agentIdentity(r api.Request, token string) (kind, name string) {
	if found, ok := agents.Lookup(r.Agent.Kind); ok {
		return found.ID, found.Name
	}
	if found, ok := agents.Identify(r.Agent.Client); ok {
		return found.ID, found.Name
	}
	if r.Agent.Client != "" {
		return "", r.Agent.Client
	}
	return "", token
}

// touchAgent records a call or heartbeat from the agent behind r. The caller holds b.agent.mu.
func (b *APIBackend) touchAgent(r api.Request, token string, now int64, call bool) *agentSession {
	key := r.Agent.Session
	if key == "" {
		// Older lgmon mcp and plain HTTP callers send no session: one token is one connection.
		key = "token\x00" + token
	}
	if b.agent.sessions == nil {
		b.agent.sessions = map[string]*agentSession{}
	}
	session := b.agent.sessions[key]
	if session == nil || session.closed {
		b.pruneAgents(now)
		sum := sha256.Sum256([]byte(key))
		session = &agentSession{AgentSession: api.AgentSession{ID: hex.EncodeToString(sum[:6]), Since: now}}
		b.agent.sessions[key] = session
	}
	session.Kind, session.Name = agentIdentity(r, token)
	if r.Agent.Client != "" {
		session.Client, session.Version = r.Agent.Client, r.Agent.Version
	}
	session.Token, session.LastSeenAt = token, now
	if call {
		session.Calls++
	}
	return session
}

// pruneAgents drops connections gone for a day, then the oldest ones beyond the limit. The caller holds b.agent.mu.
func (b *APIBackend) pruneAgents(now int64) {
	for key, session := range b.agent.sessions {
		if now-session.LastSeenAt > agentKeep.Milliseconds() {
			delete(b.agent.sessions, key)
		}
	}
	for len(b.agent.sessions) >= agentSessionLimit {
		oldest := ""
		for key, session := range b.agent.sessions {
			if oldest == "" || session.LastSeenAt < b.agent.sessions[oldest].LastSeenAt {
				oldest = key
			}
		}
		delete(b.agent.sessions, oldest)
	}
}

func (b *APIBackend) agentPresence(ctx context.Context, r api.Request) (any, error) {
	var input api.AgentPresenceInput
	if err := api.DecodeBody(r.Body, &input); err != nil {
		return nil, err
	}
	if input.State == "" {
		input.State = "alive"
	}
	if !slices.Contains([]string{"open", "alive", "closed"}, input.State) || input.Interval < 0 || input.Interval > 600 {
		return nil, invalidAPI("state 必须是 open、alive 或 closed，interval 在 0～600 秒之间")
	}
	if r.Via != "mcp" {
		// Only an MCP server reports for an agent; the web page and scripts are not agents.
		return api.Confirmation{OK: true, Message: "不是 MCP 连接，没有记录"}, nil
	}
	principal, _ := api.CurrentPrincipal(ctx)
	now := b.options.Now().UnixMilli()
	b.agent.mu.Lock()
	session := b.touchAgent(r, principal.Name, now, false)
	session.interval = input.Interval
	session.closed = input.State == "closed"
	b.agent.mu.Unlock()
	if input.State == "open" {
		b.noteAgentSeen(now, session.Name)
	}
	return api.Confirmation{OK: true, Message: "已记录"}, nil
}

// listAgents puts connected agents first, newest first within each group.
func (b *APIBackend) listAgents() []api.AgentSession {
	now := b.options.Now().UnixMilli()
	b.agent.mu.Lock()
	defer b.agent.mu.Unlock()
	result := []api.AgentSession{}
	for _, session := range b.agent.sessions {
		if now-session.LastSeenAt > agentKeep.Milliseconds() {
			continue
		}
		item := session.AgentSession
		item.Connected = session.connected(now)
		result = append(result, item)
	}
	slices.SortFunc(result, func(a, b api.AgentSession) int {
		if a.Connected != b.Connected {
			if a.Connected {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(b.LastSeenAt, a.LastSeenAt), strings.Compare(a.ID, b.ID))
	})
	return result
}
