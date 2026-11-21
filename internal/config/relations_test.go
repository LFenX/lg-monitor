// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package config

import (
	"strings"
	"testing"
)

func TestDependenciesMustPointAtOtherExistingServicesOnce(t *testing.T) {
	base := func(site Target) *Config {
		site.ID, site.Match = "site", Match{Process: "node.exe"}
		return &Config{Targets: []Target{site, {ID: "db", Match: Match{Service: "postgresql"}}}}
	}
	ok := base(Target{Role: "web", Description: "虚构网站", Depends: []Dependency{{Target: "db", Relation: "data", Note: "订单表"}}})
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	for want, site := range map[string]Target{
		"不能依赖它自己":  {Depends: []Dependency{{Target: "site"}}},
		"不存在":      {Depends: []Dependency{{Target: "ghost"}}},
		"重复":       {Depends: []Dependency{{Target: "db"}, {Target: "db"}}},
		"relation": {Depends: []Dependency{{Target: "db", Relation: "owns"}}},
		"role":     {Role: "spaceship"},
		"最多 300":   {Description: strings.Repeat("长", 301)},
		"补充说明":     {Depends: []Dependency{{Target: "db", Note: strings.Repeat("长", 81)}}},
	} {
		if err := Validate(base(site)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestCollectorsDoNotReceiveWhatServicesSayAboutThemselves(t *testing.T) {
	cfg := &Config{Targets: []Target{{ID: "site", Match: Match{Process: "node.exe"}, Role: "web", Description: "虚构网站", Depends: []Dependency{{Target: "db"}}}, {ID: "db", Match: Match{Service: "postgresql"}}}}
	_, data, err := TargetConfig(cfg, "fake-host", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"role", "description", "depends"} {
		if strings.Contains(string(data), `"`+field+`"`) {
			t.Fatalf("%s sent to the collector: %s", field, data)
		}
	}
}
