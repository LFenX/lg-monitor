// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"strconv"
	"strings"
)

// LANFirewall opens the LAN sharing ports while sharing is on. Windows never asks about a service's
// listening sockets, and on a network marked Public it blocks them even for allowed apps, so without a
// rule other computers can neither discover this one nor send a request.
type LANFirewall interface {
	Allow(context.Context, LANFirewallRule) error
	Remove(context.Context) error
}

// LANFirewallRule is what one lgmon needs: its member port over TCP, discovery over UDP, and optional
// read-only LAN web dashboard port over TCP, for its own program only.
type LANFirewallRule struct {
	Program string
	TCPPort int
	UDPPort int
	WebPort int
}

// lanFirewallName is shared by all rules so one delete removes them, and stays ASCII so every
// localized netsh accepts it.
const lanFirewallName = "lgmon LAN sharing"

// lanFirewallRemote limits the rules to private, link-local and Tailscale addresses: other people on the
// same network, never the internet.
const lanFirewallRemote = "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,100.64.0.0/10,fc00::/7,fe80::/10"

// lanFirewallArgs are the netsh arguments that add the rule for one protocol.
func lanFirewallArgs(rule LANFirewallRule, protocol string, port int) []string {
	return []string{"advfirewall", "firewall", "add", "rule", "name=" + lanFirewallName, "dir=in", "action=allow",
		"program=" + rule.Program, "protocol=" + protocol, "localport=" + strconv.Itoa(port),
		"remoteip=" + lanFirewallRemote, "profile=any", "enable=yes"}
}

func lanFirewallRuleSets(rule LANFirewallRule) [][]string {
	var sets [][]string
	if rule.TCPPort > 0 {
		sets = append(sets, lanFirewallArgs(rule, "TCP", rule.TCPPort))
	}
	if rule.UDPPort > 0 {
		sets = append(sets, lanFirewallArgs(rule, "UDP", rule.UDPPort))
	}
	if rule.WebPort > 0 && rule.WebPort != rule.TCPPort {
		sets = append(sets, lanFirewallArgs(rule, "TCP", rule.WebPort))
	}
	return sets
}

// lanFirewallFix is the same change as commands a person can paste into an administrator PowerShell.
func lanFirewallFix(rule LANFirewallRule) string {
	quote := func(arg string) string {
		if name, value, ok := strings.Cut(arg, "="); ok && strings.ContainsAny(value, " ()") {
			return name + `="` + value + `"`
		}
		return arg
	}
	lines := []string{"netsh advfirewall firewall delete rule name=\"" + lanFirewallName + "\""}
	for _, args := range lanFirewallRuleSets(rule) {
		quoted := make([]string, len(args))
		for index, arg := range args {
			quoted[index] = quote(arg)
		}
		lines = append(lines, "netsh "+strings.Join(quoted, " "))
	}
	return strings.Join(lines, "\n")
}
