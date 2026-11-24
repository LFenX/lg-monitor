// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/transport"
)

// discoveryTargets adds direct questions to the broadcast ones. Broadcast stops at the subnet, while office
// networks are often several routed subnets: the hosts someone remembered, asked for a board, or reached a
// department on are asked directly, so they keep showing up.
func (m *TeamManager) discoveryTargets(ctx context.Context, port int) []net.Addr {
	broadcast := transport.LANTargets
	if m.options.DiscoveryTargets != nil {
		broadcast = m.options.DiscoveryTargets
	}
	targets := broadcast(port)
	var hosts []string
	if m.options.Config != nil {
		hosts = append(hosts, m.options.Config.Snapshot().Settings.LAN.Peers...)
	}
	if repository, err := m.lanRepository(); err == nil {
		if outgoing, err := repository.LANOutgoing(ctx); err == nil {
			for _, item := range outgoing {
				hosts = append(hosts, item.Address)
			}
		}
	}
	m.dept.mu.Lock()
	for _, reach := range m.dept.reach {
		hosts = append(hosts, reach.address)
	}
	m.dept.mu.Unlock()
	return append(targets, peerTargets(ctx, hosts, port)...)
}

func peerTargets(ctx context.Context, hosts []string, port int) []net.Addr {
	seen := map[netip.Addr]bool{}
	var result []net.Addr
	add := func(address netip.Addr) {
		address = address.Unmap()
		if !address.Is4() || address.IsUnspecified() || seen[address] {
			return
		}
		seen[address] = true
		result = append(result, &net.UDPAddr{IP: address.AsSlice(), Port: port})
	}
	for _, value := range hosts {
		host, err := config.LANPeerHost(value)
		if err != nil {
			continue
		}
		if address, err := netip.ParseAddr(host); err == nil {
			add(address)
			continue
		}
		// A slow name server must not hold up discovery for everyone else.
		lookup, cancel := context.WithTimeout(ctx, time.Second)
		addresses, err := net.DefaultResolver.LookupNetIP(lookup, "ip4", host)
		cancel()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			add(address)
		}
	}
	return result
}

func departmentProblem(err error) string {
	var fault *api.Fault
	if errors.As(err, &fault) && fault.Message != "" {
		return fault.Message
	}
	return "连不上托管这个部门的电脑"
}
