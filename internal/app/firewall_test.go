// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/transport"
)

type fakeFirewall struct {
	mu      sync.Mutex
	allowed []LANFirewallRule
	removes int
	fail    error
}

func (f *fakeFirewall) Allow(_ context.Context, rule LANFirewallRule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.allowed = append(f.allowed, rule)
	return f.fail
}

func (f *fakeFirewall) Remove(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removes++
	return nil
}

func (f *fakeFirewall) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.allowed), f.removes
}

// addressListener only reports an address, so the firewall logic sees a port open to the network
// without the test opening one.
type addressListener struct{ address net.Addr }

func (l addressListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (l addressListener) Close() error              { return nil }
func (l addressListener) Addr() net.Addr            { return l.address }

func TestLANFirewallFollowsSharing(t *testing.T) {
	f := newAPIFixture(t)
	setVisible := func(visible bool) {
		data := "lan:\n  name: 虚构负责人\n"
		if visible {
			data += "  visible: true\n"
		}
		f.files.MapFS["settings.yaml"] = &fstest.MapFile{Data: []byte(data)}
		if err := f.manager.Reload(); err != nil {
			t.Fatal(err)
		}
	}
	setVisible(false)
	keys := &teamTestKeys{values: map[string][]byte{}}
	clock := f.now
	now := func() time.Time { return clock }
	authority, err := transport.NewAuthority(context.Background(), transport.AuthorityOptions{Keys: keys, State: transport.FileState{Home: t.TempDir()}, SANs: teamTestSANs{}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	firewall := &fakeFirewall{}
	const program = `C:\Program Files\lg-monitor\lgmon.exe`
	manager, err := NewTeamManager(TeamOptions{Config: f.manager, Repository: f.authDB, Authority: authority, Queries: f.sqlView, Keys: keys, Now: now, Hostname: "fake-host", Firewall: firewall, Program: program, DiscoveryPort: 7446})
	if err != nil {
		t.Fatal(err)
	}
	node := &TeamNode{manager: manager, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), current: &teamListener{listener: addressListener{&net.TCPAddr{IP: net.IPv4zero, Port: 7445}}}}
	manager.firewall, manager.listening = node.firewallState, func() (string, error) { return "0.0.0.0:7445", nil }
	ctx := context.Background()
	sync := func() { node.syncFirewall(ctx, f.manager.Snapshot().Settings) }

	// A rule left by an earlier run is cleared once when sharing starts off, and nothing is opened.
	sync()
	sync()
	if allowed, removes := firewall.counts(); allowed != 0 || removes != 1 {
		t.Fatalf("hidden start allowed=%d removes=%d", allowed, removes)
	}

	setVisible(true)
	sync()
	sync()
	if allowed, _ := firewall.counts(); allowed != 1 || firewall.allowed[0] != (LANFirewallRule{Program: program, TCPPort: 7445, UDPPort: 7446}) {
		t.Fatalf("sharing on opened %+v", firewall.allowed)
	}
	state, err := manager.LANState(ctx)
	if err != nil || state.Firewall != "allowed" || state.FirewallFix != "" || state.Problem != "" {
		t.Fatalf("allowed state=%+v %v", state, err)
	}

	setVisible(false)
	sync()
	if _, removes := firewall.counts(); removes != 2 {
		t.Fatalf("sharing off did not close the ports: removes=%d", removes)
	}

	// Without administrator rights the page says so and gives the exact commands, and lgmon does not
	// retry netsh every second.
	firewall.fail = errors.New("需要管理员")
	setVisible(true)
	sync()
	sync()
	if allowed, _ := firewall.counts(); allowed != 2 {
		t.Fatalf("a failed attempt was retried at once: %d", allowed)
	}
	state, _ = manager.LANState(ctx)
	if state.Firewall != "blocked" || !strings.Contains(state.Problem, "防火墙") {
		t.Fatalf("blocked state=%+v", state)
	}
	for _, want := range []string{`name="lgmon LAN sharing"`, `program="` + program + `"`, "protocol=TCP localport=7445", "protocol=UDP localport=7446", "remoteip=10.0.0.0/8,", "profile=any"} {
		if !strings.Contains(state.FirewallFix, want) {
			t.Fatalf("fix lacks %q:\n%s", want, state.FirewallFix)
		}
	}
	firewall.fail = nil
	clock = clock.Add(lanFirewallRetry)
	sync()
	if state, _ = manager.LANState(ctx); state.Firewall != "allowed" {
		t.Fatalf("retry after the wait did not open the ports: %+v", state)
	}

	// A member port bound to this computer only needs no rule.
	node.current = &teamListener{listener: addressListener{&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7445}}}
	sync()
	if _, removes := firewall.counts(); removes != 3 {
		t.Fatalf("a loopback port kept the rule: removes=%d", removes)
	}
}
