//go:build !windows

package app

import "context"

// NativeLANFirewall does nothing outside Windows; other systems' firewalls are left to their owners.
type NativeLANFirewall struct{}

func (NativeLANFirewall) Allow(context.Context, LANFirewallRule) error { return nil }

func (NativeLANFirewall) Remove(context.Context) error { return nil }
