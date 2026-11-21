//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import "context"

// NativeLANFirewall does nothing outside Windows; other systems' firewalls are left to their owners.
type NativeLANFirewall struct{}

func (NativeLANFirewall) Allow(context.Context, LANFirewallRule) error { return nil }

func (NativeLANFirewall) Remove(context.Context) error { return nil }
