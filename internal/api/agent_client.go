package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/LFenX/lg-monitor/internal/agents"
)

// The MCP server running for an agent says who the agent is in these headers on every call it makes.
const (
	headerAgentKind     = "X-Lgmon-Agent"
	headerAgentClient   = "X-Lgmon-Client"
	headerAgentVersion  = "X-Lgmon-Client-Version"
	headerAgentSession  = "X-Lgmon-Session"
	agentHeaderMaxBytes = 80
)

// headerText keeps printable ASCII only: header values cannot carry anything else, and these are only labels.
func headerText(value string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if r >= 0x20 && r < 0x7f && b.Len() < agentHeaderMaxBytes {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func agentFromHeader(header http.Header) AgentClient {
	return AgentClient{
		Kind:    headerText(header.Get(headerAgentKind)),
		Client:  headerText(header.Get(headerAgentClient)),
		Version: headerText(header.Get(headerAgentVersion)),
		Session: headerText(header.Get(headerAgentSession)),
	}
}

func setAgentHeader(header http.Header, agent AgentClient) {
	for name, value := range map[string]string{headerAgentKind: agent.Kind, headerAgentClient: agent.Client, headerAgentVersion: agent.Version, headerAgentSession: agent.Session} {
		if value = headerText(value); value != "" {
			header.Set(name, value)
		} else {
			header.Del(name)
		}
	}
}

// mcpClientInfo reads who the agent is out of an MCP request without consuming the body. The handshake protocol
// says it once in initialize; the sessionless protocol (2026-07-28) says it in every request's _meta and opens
// with server/discover. opening reports either first request.
func mcpClientInfo(request *http.Request) (client AgentClient, opening, found bool) {
	if request.Method != http.MethodPost || request.Body == nil {
		return AgentClient{}, false, false
	}
	head, err := io.ReadAll(io.LimitReader(request.Body, MaxRequestBody))
	request.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), request.Body), request.Body}
	if err != nil || !bytes.Contains(head, []byte(`clientInfo`)) && !bytes.Contains(head, []byte(`server/discover`)) {
		return AgentClient{}, false, false
	}
	type implementation struct {
		Name    string `json:"name"`
		Title   string `json:"title"`
		Version string `json:"version"`
	}
	var message struct {
		Method string `json:"method"`
		Params struct {
			ClientInfo implementation `json:"clientInfo"`
			Meta       struct {
				ClientInfo implementation `json:"io.modelcontextprotocol/clientInfo"`
			} `json:"_meta"`
		} `json:"params"`
	}
	if json.Unmarshal(head, &message) != nil {
		return AgentClient{}, false, false
	}
	info := message.Params.Meta.ClientInfo
	if message.Method == "initialize" {
		info = message.Params.ClientInfo
	}
	opening = message.Method == "initialize" || message.Method == "server/discover"
	name := info.Name
	if name == "" {
		name = info.Title
	}
	if name == "" {
		return AgentClient{}, opening, false
	}
	client = AgentClient{Client: name, Version: info.Version}
	if kind, ok := agents.Identify(info.Name, info.Title, request.UserAgent()); ok {
		client.Kind = kind.ID
	}
	return client, opening, true
}

// httpAgentSession names an agent that talks to /mcp directly: HTTP MCP here is stateless, so one agent
// using one token is one connection.
func httpAgentSession(token, client string) string {
	sum := sha256.Sum256([]byte(token + "\x00" + client))
	return "http-" + hex.EncodeToString(sum[:8])
}
