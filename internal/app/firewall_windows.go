//go:build windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// NativeLANFirewall changes Windows Defender Firewall through netsh, which every Windows 10/11 has and
// which needs no COM. It works when lgmon runs as the service or from an administrator terminal.
type NativeLANFirewall struct{}

func (NativeLANFirewall) Allow(ctx context.Context, rule LANFirewallRule) error {
	if err := (NativeLANFirewall{}).Remove(ctx); err != nil {
		return err
	}
	for _, args := range [][]string{lanFirewallArgs(rule, "TCP", rule.TCPPort), lanFirewallArgs(rule, "UDP", rule.UDPPort)} {
		if output, err := netsh(ctx, args...); err != nil {
			return fmt.Errorf("没有放行 Windows 防火墙（需要管理员权限）：%w %s", err, output)
		}
	}
	return nil
}

func (NativeLANFirewall) Remove(ctx context.Context) error {
	// "show" fails when no rule has the name; only a rule that exists is worth deleting.
	if _, err := netsh(ctx, "advfirewall", "firewall", "show", "rule", "name="+lanFirewallName); err != nil {
		return nil
	}
	if output, err := netsh(ctx, "advfirewall", "firewall", "delete", "rule", "name="+lanFirewallName); err != nil {
		return fmt.Errorf("没有删除 Windows 防火墙规则（需要管理员权限）：%w %s", err, output)
	}
	return nil
}

func netsh(parent context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "netsh", args...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	output, err := command.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}
