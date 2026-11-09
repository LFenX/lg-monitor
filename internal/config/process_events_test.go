package config

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestProcessEventModeDefaultsAndLocatedValidation(t *testing.T) {
	for _, tc := range []struct {
		mode    string
		enabled bool
	}{{"", true}, {"auto", true}, {"disabled", false}} {
		enabled, err := (ProcessEventsSettings{Mode: tc.mode}).Enabled()
		if err != nil || enabled != tc.enabled {
			t.Fatal(tc, enabled, err)
		}
	}
	for _, mode := range []string{"poll", "wmi", "etw", "AUTO"} {
		_, err := Load(fstest.MapFS{"settings.yaml": {Data: []byte("processEvents:\n  mode: " + mode + "\n")}})
		if err == nil || !strings.Contains(err.Error(), "settings.yaml 第 2 行") || !strings.Contains(err.Error(), "processEvents.mode") {
			t.Fatal("invalid or unlocated event mode", mode, err)
		}
	}
}

func TestProcessEventSchemaMatchesDefaultsAndAcceptedModes(t *testing.T) {
	schema := compiledSchemas(t)["settings.schema.json"]
	for _, mode := range []string{"", "auto", "disabled"} {
		if err := schema.Validate(map[string]any{"processEvents": map[string]any{"mode": mode}}); err != nil {
			t.Fatal(mode, err)
		}
	}
	if err := schema.Validate(map[string]any{"processEvents": map[string]any{"mode": "wmi"}}); err == nil {
		t.Fatal("schema accepted an unavailable named provider")
	}
}
