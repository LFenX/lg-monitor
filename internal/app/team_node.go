// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/transport"
)

type teamSocket struct{ member, team string }
type teamListener struct {
	server   *http.Server
	listener net.Listener
	tls      atomic.Pointer[tls.Config]
	done     chan struct{}
	address  string
}
type TeamNode struct {
	manager      *TeamManager
	network      HTTPNetwork
	logger       *slog.Logger
	ready        func(string)
	mu           sync.Mutex
	current      *teamListener
	lastErr      error
	connections  map[net.Conn]teamSocket
	connectionMu sync.Mutex
	cancel       context.CancelFunc
	done         chan struct{}
	// discovery answers LAN "who is here" questions while the board list is visible.
	discoveryStop context.CancelFunc
	discoveryDone chan struct{}
	discoveryErr  error
	// firewall is the rule last applied for LAN sharing and how the last attempt went.
	firewallMu      sync.Mutex
	firewallRule    *LANFirewallRule
	firewallErr     error
	firewallTried   time.Time
	firewallSettled bool
}

// lanFirewallState is what the LAN page says about the firewall.
type lanFirewallState struct {
	want    *LANFirewallRule
	applied bool
	err     error
}

// lanFirewallRetry spaces out attempts that failed, usually for lack of administrator rights.
const lanFirewallRetry = 5 * time.Minute

// wantedFirewall is the rule LAN sharing and/or read-only LAN web viewing needs now, or nil when nothing should be open.
func (n *TeamNode) wantedFirewall(settings config.Settings) *LANFirewallRule {
	n.mu.Lock()
	current := n.current
	n.mu.Unlock()
	var tcp, udp, webPort int
	if settings.LAN.Present() && current != nil {
		host, port, err := net.SplitHostPort(current.listener.Addr().String())
		if ip := net.ParseIP(host); err == nil && (ip == nil || !ip.IsLoopback()) {
			tcp, _ = strconv.Atoi(port)
			udp = n.manager.options.DiscoveryPort
			if udp == 0 {
				udp = transport.LANDiscoveryPort
			}
		}
	}
	if settings.LAN.Web {
		host, port, err := net.SplitHostPort(settings.HTTPAddress())
		if err == nil {
			ip := net.ParseIP(strings.Trim(host, "[]"))
			if ip == nil || !ip.IsLoopback() {
				webPort, _ = strconv.Atoi(port)
			}
		}
	}
	if tcp == 0 && udp == 0 && webPort == 0 {
		return nil
	}
	program := n.manager.options.Program
	if program == "" {
		program, _ = os.Executable()
	}
	return &LANFirewallRule{Program: program, TCPPort: tcp, UDPPort: udp, WebPort: webPort}
}

// syncFirewall keeps the LAN rule in step with sharing: added when the member port is reachable from
// the network, removed when sharing is off. A rule left by an earlier run is removed once at start.
func (n *TeamNode) syncFirewall(ctx context.Context, settings config.Settings) {
	firewall := n.manager.options.Firewall
	if firewall == nil {
		return
	}
	want := n.wantedFirewall(settings)
	n.firewallMu.Lock()
	applied, tried, settled, failed := n.firewallRule, n.firewallTried, n.firewallSettled, n.firewallErr != nil
	n.firewallMu.Unlock()
	now := n.manager.options.Now()
	var err error
	switch {
	case want == nil && (applied != nil || !settled):
		err = firewall.Remove(ctx)
		applied = nil
	case want != nil && (applied == nil || *applied != *want) && (!failed || !now.Before(tried.Add(lanFirewallRetry)) || applied != nil):
		if err = firewall.Allow(ctx, *want); err == nil {
			applied = want
		} else {
			applied = nil
		}
	default:
		return
	}
	if err != nil && ctx.Err() == nil {
		n.logger.Warn("LAN firewall rule not changed", "error", err)
	}
	n.firewallMu.Lock()
	n.firewallRule, n.firewallErr, n.firewallTried, n.firewallSettled = applied, err, now, true
	n.firewallMu.Unlock()
	if err == nil {
		n.manager.notify()
	}
}

// firewallState reports the rule sharing needs and whether it is in place.
func (n *TeamNode) firewallState() lanFirewallState {
	want := n.wantedFirewall(n.manager.options.Config.Snapshot().Settings)
	n.firewallMu.Lock()
	defer n.firewallMu.Unlock()
	return lanFirewallState{want: want, applied: want != nil && n.firewallRule != nil && *n.firewallRule == *want, err: n.firewallErr}
}

func NewTeamNode(parent context.Context, manager *TeamManager, network HTTPNetwork, logger *slog.Logger, ready func(string)) (*TeamNode, error) {
	if manager == nil {
		return nil, fmt.Errorf("团队监听需要管理器")
	}
	if network == nil {
		network = NativeHTTPNetwork{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(parent)
	n := &TeamNode{manager: manager, network: network, logger: logger, ready: ready, connections: map[net.Conn]teamSocket{}, cancel: cancel, done: make(chan struct{})}
	manager.mu.Lock()
	manager.address = n.publicAddress
	manager.disconnect = n.disconnect
	manager.listening = n.listening
	manager.firewall = n.firewallState
	manager.mu.Unlock()
	go n.run(ctx)
	return n, nil
}

func (n *TeamNode) publicAddress(ctx context.Context) (string, error) {
	n.manager.notify()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		n.mu.Lock()
		endpoint := ""
		if n.current != nil {
			endpoint = n.current.address
		}
		failure := n.lastErr
		n.mu.Unlock()
		if endpoint != "" {
			return endpoint, nil
		}
		if failure != nil {
			return "", fmt.Errorf("团队成员端口暂不可用，请检查 teamListen 后重试：%w", failure)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-n.done:
			return "", fmt.Errorf("团队监听已结束")
		case <-timer.C:
			return "", fmt.Errorf("团队成员入口正在启动，请稍后重试")
		case <-tick.C:
		}
	}
}

// listening reports the member port's actual listen address and the last start failure.
func (n *TeamNode) listening() (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.current != nil {
		return n.current.listener.Addr().String(), nil
	}
	if n.lastErr != nil {
		return "", n.lastErr
	}
	return "", n.discoveryErr
}

func (n *TeamNode) beacon() (transport.LANBeacon, bool) {
	lan := n.manager.options.Config.Snapshot().Settings.LAN
	if !lan.Present() {
		return transport.LANBeacon{}, false
	}
	n.mu.Lock()
	current := n.current
	n.mu.Unlock()
	if current == nil {
		return transport.LANBeacon{}, false
	}
	_, port, _ := net.SplitHostPort(current.listener.Addr().String())
	number, _ := strconv.Atoi(port)
	return transport.LANBeacon{Name: n.manager.lanName(), Port: number, Fingerprint: n.manager.options.Authority.Fingerprint()[:16], Private: !lan.Visible, Departments: lan.Departments}, true
}

func (n *TeamNode) stopDiscovery() {
	if n.discoveryStop != nil {
		n.discoveryStop()
		<-n.discoveryDone
		n.discoveryStop, n.discoveryDone = nil, nil
	}
}

// syncDiscovery runs the discovery responder exactly while the board list is visible and the
// member port is up; a failure to bind is remembered for the status page and retried.
func (n *TeamNode) syncDiscovery(ctx context.Context, settings config.Settings) {
	n.mu.Lock()
	up := n.current != nil
	n.mu.Unlock()
	want := settings.LAN.Present() && up
	if !want {
		n.stopDiscovery()
		n.discoveryErr = nil
		return
	}
	if n.discoveryStop != nil {
		return
	}
	packets := n.manager.options.Packets
	if packets == nil {
		packets = NativePacketNetwork{}
	}
	port := n.manager.options.DiscoveryPort
	if port == 0 {
		port = transport.LANDiscoveryPort
	}
	// A member port bound to this computer only cannot serve the network, so discovery stays local too.
	bind := "0.0.0.0"
	if host, _, err := net.SplitHostPort(settings.TeamAddress()); err == nil {
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			bind = host
		}
	}
	conn, err := packets.ListenPacket(ctx, net.JoinHostPort(bind, strconv.Itoa(port)))
	if err != nil {
		if n.discoveryErr == nil {
			n.logger.Warn("LAN discovery could not start; retrying", "error", err)
		}
		n.discoveryErr = fmt.Errorf("内网发现端口 %d 打不开：%w", port, err)
		return
	}
	n.discoveryErr = nil
	discovery, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	n.discoveryStop, n.discoveryDone = stop, done
	go func() {
		defer close(done)
		if err := transport.ServeLANDiscovery(discovery, conn, n.beacon); err != nil {
			n.logger.Warn("LAN discovery stopped", "error", err)
		}
	}()
}

func (n *TeamNode) disconnect(member, team string) {
	n.connectionMu.Lock()
	connections := []net.Conn{}
	for connection, identity := range n.connections {
		if (member == "" || member == identity.member) && (team == "" || team == identity.team) {
			connections = append(connections, connection)
		}
	}
	n.connectionMu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
}

func (n *TeamNode) connectionState(connection net.Conn, state http.ConnState) {
	if state == http.StateClosed || state == http.StateHijacked {
		n.connectionMu.Lock()
		delete(n.connections, connection)
		n.connectionMu.Unlock()
		return
	}
	if state != http.StateActive {
		return
	}
	secure, ok := connection.(*tls.Conn)
	if !ok {
		return
	}
	peer := secure.ConnectionState().PeerCertificates
	if len(peer) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	member, err := n.manager.options.Repository.TeamMember(ctx, peer[0].Subject.CommonName, peer[0].SerialNumber.String())
	if err != nil {
		return
	}
	n.connectionMu.Lock()
	n.connections[connection] = teamSocket{member: member.ID, team: member.TeamID}
	n.connectionMu.Unlock()
}

func (n *TeamNode) certificate(ctx context.Context, settings config.Settings) (*tls.Config, error) {
	host, _, _ := net.SplitHostPort(settings.TeamAddress())
	hosts := []string{host}
	if settings.TeamPublicURL != "" {
		u, _ := url.Parse(settings.TeamPublicURL)
		hosts = append(hosts, u.Hostname())
	}
	return n.manager.options.Authority.TeamTLS(ctx, hosts, n.manager.verifyMember)
}

func (n *TeamNode) start(ctx context.Context, settings config.Settings) (*teamListener, error) {
	configuration, err := n.certificate(ctx, settings)
	if err != nil {
		return nil, err
	}
	listener, err := n.network.Listen(ctx, settings.TeamAddress())
	if err != nil {
		return nil, fmt.Errorf("监听团队成员端口失败：%w", err)
	}
	instance := &teamListener{listener: listener, done: make(chan struct{})}
	instance.tls.Store(configuration)
	base := &tls.Config{MinVersion: tls.VersionTLS13, GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		current := instance.tls.Load()
		if current.GetConfigForClient != nil {
			return current.GetConfigForClient(hello)
		}
		return current, nil
	}}
	address := settings.TeamPublicURL
	if address == "" {
		host, port, _ := net.SplitHostPort(listener.Addr().String())
		if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
			host = "127.0.0.1"
		}
		address = "https://" + net.JoinHostPort(host, port)
	}
	instance.address = strings.TrimSuffix(address, "/")
	instance.server = &http.Server{Handler: n.manager.memberMux(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024, ConnState: n.connectionState, ErrorLog: log.New(httpLog{n.logger}, "", 0), BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() { _ = instance.server.Serve(tls.NewListener(listener, base)); close(instance.done) }()
	if n.ready != nil {
		n.ready(listener.Addr().String())
	}
	return instance, nil
}

func stopTeamListener(instance *teamListener) {
	if instance == nil {
		return
	}
	_ = instance.server.Close()
	<-instance.done
}

func (n *TeamNode) run(ctx context.Context) {
	defer close(n.done)
	defer func() { n.mu.Lock(); current := n.current; n.current = nil; n.mu.Unlock(); stopTeamListener(current) }()
	defer n.stopDiscovery()
	updates := n.manager.options.Config.Subscribe(ctx)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var applied config.Settings
	var lastCertificate time.Time
	for {
		if n.manager.options.Config.Snapshot().Settings.LAN.Present() {
			if err := n.manager.EnsureLANTeam(ctx); err != nil && ctx.Err() == nil {
				n.logger.Warn("LAN team could not be prepared; retrying", "error", err)
			}
		}
		teams, err := n.manager.options.Repository.Teams(ctx)
		if err == nil {
			settings := n.manager.options.Config.Snapshot().Settings
			n.mu.Lock()
			current := n.current
			n.mu.Unlock()
			if len(teams) == 0 {
				if current != nil {
					n.mu.Lock()
					n.current = nil
					n.mu.Unlock()
					stopTeamListener(current)
				}
			} else if current == nil || settings.TeamAddress() != applied.TeamAddress() {
				next, failure := n.start(ctx, settings)
				n.mu.Lock()
				n.lastErr = failure
				if failure == nil {
					n.current = next
				}
				n.mu.Unlock()
				if failure == nil {
					stopTeamListener(current)
					applied = settings
					lastCertificate = n.manager.options.Now()
				}
			} else if settings.TeamPublicURL != applied.TeamPublicURL || !n.manager.options.Now().Before(lastCertificate.Add(time.Minute)) {
				configuration, failure := n.certificate(ctx, settings)
				if failure == nil {
					current.tls.Store(configuration)
					n.mu.Lock()
					if settings.TeamPublicURL != "" {
						current.address = strings.TrimSuffix(settings.TeamPublicURL, "/")
					} else {
						host, port, _ := net.SplitHostPort(current.listener.Addr().String())
						if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
							host = "127.0.0.1"
						}
						current.address = "https://" + net.JoinHostPort(host, port)
					}
					n.mu.Unlock()
					applied = settings
					lastCertificate = n.manager.options.Now()
				}
			}
		} else if ctx.Err() != nil {
			return
		}
		settings := n.manager.options.Config.Snapshot().Settings
		n.syncDiscovery(ctx, settings)
		n.syncFirewall(ctx, settings)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-n.manager.changed:
		case _, ok := <-updates:
			if !ok {
				return
			}
		}
	}
}

func (n *TeamNode) Close() { n.cancel(); <-n.done }
