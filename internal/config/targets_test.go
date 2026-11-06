package config

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
)

func TestTargetConfigSelectsOnlyTargetsAndProbes(t *testing.T) {
	cfg := &Config{Targets: []Target{
		{ID: "all", Collector: "*", Match: Match{Process: "demo.exe"}, Probes: []Probe{{Type: "tcp", Address: "127.0.0.1:8", Interval: "1s", Timeout: "1s"}}},
		{ID: "legacy", Match: Match{Process: "demo.exe"}},
		{ID: "id", Collector: "fake-id", Match: Match{Process: "demo.exe"}},
		{ID: "host", Collector: "FAKE-HOST", Match: Match{Process: "demo.exe"}},
		{ID: "other", Collector: "other-host", Match: Match{Process: "demo.exe"}},
	}, FeishuApps: []FeishuApp{{AppID: "cli_fake_never_send", SecretRef: "fake-private-ref"}}, Rules: []Rule{{ID: "fake-private-rule"}}, Notifiers: []Notifier{{ChatID: "oc_fake_never_send"}}, Templates: []Template{{ID: "fake-private-template"}}}
	version, data, err := TargetConfig(cfg, "fake-id", "fake-host")
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"cli_fake_never_send", "fake-private", "oc_fake_never_send", "other-host"} {
		if strings.Contains(string(data), private) {
			t.Fatalf("unexpected configuration leaked: %s", private)
		}
	}
	selected, err := ParseTargetConfig(version, data)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, target := range selected.Targets {
		ids = append(ids, target.ID)
	}
	if !reflect.DeepEqual(ids, []string{"all", "legacy", "id", "host"}) || len(selected.Targets[0].Probes) != 1 || len(selected.Rules) != 0 {
		t.Fatalf("wrong target selection: %+v", selected)
	}
	cfg.Rules, cfg.Notifiers, cfg.FeishuApps, cfg.Templates = nil, nil, nil, nil
	next, _, _ := TargetConfig(cfg, "fake-id", "fake-host")
	if next != version {
		t.Fatal("monitor-only settings changed the collector version")
	}
	for _, selector := range []string{"*", "fake-id", "FAKE-HOST", "中文主机"} {
		target := Target{ID: "demo", Collector: selector, Match: Match{Process: "demo"}}
		if err := Validate(&Config{Targets: []Target{target}}); err != nil {
			t.Fatalf("valid collector %q: %v", selector, err)
		}
	}
	for _, selector := range []string{"host other", "host/*", "http://host", strings.Repeat("a", 256)} {
		if err := Validate(&Config{Targets: []Target{{ID: "demo", Collector: selector, Match: Match{Process: "demo"}}}}); err == nil {
			t.Fatalf("accepted invalid collector %q", selector)
		}
	}
}

func TestPushedTargetsRejectInvalidContentAndVersions(t *testing.T) {
	for _, data := range []string{
		`null`, `[]`, `{} {}`, `{"rules":[]}`, `{"targets":[{"id":"broken","match":{}}]}`,
		`{"targets":[{"id":"demo","match":{"process":"demo"},"probes":[{"type":"tcp","address":"127.0.0.1:1","interval":"1s","timeout":"bad"}]}]}`,
	} {
		sum := sha256.Sum256([]byte(data))
		if _, err := ParseTargetConfig(hex.EncodeToString(sum[:]), []byte(data)); err == nil {
			t.Fatalf("accepted invalid pushed content: %s", data)
		}
	}
	if _, err := ParseTargetConfig(strings.Repeat("0", 64), []byte(`{}`)); err == nil {
		t.Fatal("accepted mismatched configuration version")
	}
}
