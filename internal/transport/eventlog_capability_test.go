package transport

import (
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/config"
)

func TestEventLogConfigurationRequiresCapabilityOnlyForAssignedSubscriptions(t *testing.T) {
	cfg := &config.Config{Targets: []config.Target{{ID: "fake-log", Collector: "legacy", EventLog: &config.EventLogSubscription{Channel: "Application", Provider: "Fake"}}}}
	if reason := missingConfigCapability(cfg, Hello{CollectorID: "legacy"}); !strings.Contains(reason, "fake-log") || !strings.Contains(reason, EventLogCapability) {
		t.Fatalf("missing version diagnosis: %s", reason)
	}
	if reason := missingConfigCapability(cfg, Hello{CollectorID: "other"}); reason != "" {
		t.Fatal("unassigned collector blocked")
	}
	if reason := missingConfigCapability(cfg, Hello{CollectorID: "legacy", Capabilities: []string{EventLogCapability}}); reason != "" {
		t.Fatal("supported collector blocked")
	}
	cfg.Targets[0].EventLog = nil
	cfg.Targets[0].Match.Process = "fake.exe"
	if reason := missingConfigCapability(cfg, Hello{CollectorID: "legacy"}); reason != "" {
		t.Fatal("M1 process target stopped working")
	}
}
