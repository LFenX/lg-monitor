// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package osinfo

import (
	"runtime"
	"testing"
)

func TestMachineKeyIsStableAndHidesTheSystemIdentifier(t *testing.T) {
	const guid = "6F9619FF-8B86-D011-B42D-00C04FC964FF"
	key := machineKey(guid, "cloud", "")
	if len(key) != 32 || key == "6f9619ff8b86d011b42d00c04fc964ff" {
		t.Fatalf("unexpected key %q", key)
	}
	if machineKey("6f9619ff8b86d011b42d00c04fc964ff\n", "cloud", "") != key {
		t.Fatal("the same identifier in another spelling must give the same key")
	}
	if machineKey(guid, "cloud", `d:\dev-home`) == key || machineKey(guid, "license", "") == key {
		t.Fatal("another instance or purpose must give another key")
	}
	for _, unusable := range []string{"", "uninitialized", "00000000000000000000000000000000", "1234"} {
		if machineKey(unusable, "cloud", "") != "" {
			t.Fatalf("%q must not identify a computer", unusable)
		}
	}
}

func TestMachineKeyReadsThisSystem(t *testing.T) {
	if runtime.GOOS == "windows" && MachineKey("cloud", "") == "" {
		t.Fatal("Windows always has a MachineGuid")
	}
}
