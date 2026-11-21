// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import "testing"

func TestIsVirtualInterfaceName(t *testing.T) {
	virtuals := []string{
		"vEthernet (WSL (Hyper-V firewall))",
		"vEthernet (Default Switch)",
		"VMware Network Adapter VMnet8",
		"Tailscale",
		"CorpLink TAP-Windows6",
		"DockerNAT",
		"Meta",
	}
	for _, name := range virtuals {
		if name == "Meta" {
			continue
		}
		if !isVirtualInterfaceName(name) {
			t.Errorf("expected %q to be detected as virtual", name)
		}
	}
	physicals := []string{"WLAN", "WLAN 3", "以太网", "Ethernet", "Wi-Fi"}
	for _, name := range physicals {
		if isVirtualInterfaceName(name) {
			t.Errorf("expected %q to be physical, not virtual", name)
		}
	}
}

func TestLANWebAddressesExplicitBindReturnsSingleURL(t *testing.T) {
	addrs := lanWebAddresses("10.8.61.200:7444")
	if len(addrs) != 1 || addrs[0] != "http://10.8.61.200:7444/" {
		t.Fatalf("expected single explicit LAN URL, got %v", addrs)
	}
}
