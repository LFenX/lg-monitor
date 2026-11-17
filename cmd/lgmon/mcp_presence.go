package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/agents"
	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shirou/gopsutil/v4/process"
)

// mcpPresenceInterval is how often a stdio MCP server tells lgmon its agent is still there.
const mcpPresenceInterval = 30 * time.Second

type presenceCaller interface {
	SetAgent(api.AgentClient)
	Call(context.Context, api.Operation, api.Request) (api.MCPResponse, error)
}

// mcpPresence reports the agent this stdio MCP server works for, from initialize until the agent closes it,
// so lgmon can show every connected agent with its own name.
type mcpPresence struct {
	ctx       context.Context
	client    presenceCaller
	interval  time.Duration
	ancestors func(context.Context) []agents.Process
	session   string
	once      sync.Once
	stop      chan struct{}
	done      chan struct{}
}

func newMCPPresence(ctx context.Context, client presenceCaller, interval time.Duration, ancestors func(context.Context) []agents.Process) *mcpPresence {
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	return &mcpPresence{ctx: ctx, client: client, interval: interval, ancestors: ancestors, session: "stdio-" + hex.EncodeToString(id), stop: make(chan struct{}), done: make(chan struct{})}
}

// identify runs once the agent has initialized the MCP session.
func (p *mcpPresence) identify(info mcp.Implementation) {
	p.once.Do(func() {
		agent := api.AgentClient{Client: info.Name, Version: info.Version, Session: p.session}
		if agent.Client == "" {
			agent.Client = info.Title
		}
		kind, ok := agents.Identify(info.Name, info.Title)
		if !ok && p.ancestors != nil {
			// Some MCP SDKs report a generic name such as "mcp"; the program that started us is the agent.
			kind, ok = agents.FromProcesses(p.ancestors(p.ctx))
		}
		if ok {
			agent.Kind = kind.ID
		}
		p.client.SetAgent(agent)
		go p.run()
	})
}

func (p *mcpPresence) run() {
	defer close(p.done)
	p.report(p.ctx, "open")
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.stop:
			return
		case <-ticker.C:
			p.report(p.ctx, "alive")
		}
	}
}

// report ignores failures: lgmon may be restarting, and the next heartbeat says the same thing again.
func (p *mcpPresence) report(ctx context.Context, state string) {
	body, _ := json.Marshal(api.AgentPresenceInput{State: state, Interval: int(p.interval / time.Second)})
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, _ = p.client.Call(ctx, api.AgentPresence, api.Request{Body: body})
}

// close says goodbye after the agent ended the session; lgmon would otherwise wait for missed heartbeats.
func (p *mcpPresence) close() {
	started := true
	p.once.Do(func() { started = false })
	if !started {
		return
	}
	close(p.stop)
	<-p.done
	ctx, cancel := context.WithTimeout(context.WithoutCancel(p.ctx), 3*time.Second)
	defer cancel()
	p.report(ctx, "closed")
}

// processAncestors lists the processes above this one, nearest first. A few levels are enough: past the agent
// come its terminal and the desktop.
func processAncestors(ctx context.Context) []agents.Process {
	var chain []agents.Process
	pid := int32(os.Getppid())
	for range 5 {
		if pid <= 4 {
			break
		}
		item, err := process.NewProcessWithContext(ctx, pid)
		if err != nil {
			break
		}
		executable, _ := item.ExeWithContext(ctx)
		if executable == "" {
			executable, _ = item.NameWithContext(ctx)
		}
		line, _ := item.CmdlineWithContext(ctx)
		chain = append(chain, agents.Process{Executable: executable, CommandLine: line})
		parent, err := item.PpidWithContext(ctx)
		if err != nil || parent == pid {
			break
		}
		pid = parent
	}
	return chain
}
