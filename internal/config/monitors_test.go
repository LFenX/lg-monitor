package config

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCollectorMonitorLocalConfigurationAndBounds(t *testing.T) {
	for _, id := range []string{"", "default", "con", "nul", "com1", "lpt9", "../outside", "C:\\fake", "UPPER", "a/b", "a.b", "a ", strings.Repeat("a", 33)} {
		if ValidateMonitorID(id) == nil {
			t.Fatalf("accepted unsafe ID %q", id)
		}
	}
	for _, id := range []string{"a", "company-2", strings.Repeat("a", 32)} {
		if err := ValidateMonitorID(id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	for _, tc := range []struct {
		yaml  string
		valid bool
	}{
		{"collectorMonitors: [{id: company}, {id: home, enabled: false}]", true},
		{"collectorMonitors: [{id: company}, {id: company}]", false},
		{"collectorMonitors: [{id: default}]", false},
		{"collectorMonitors: [{id: company, enabled: not-a-bool}]", false},
		{"collectorMonitors: [{id: company, monitor: untrusted}]", false},
	} {
		t.Run(tc.yaml, func(t *testing.T) {
			settings, err := LoadSettings(fstest.MapFS{"settings.yaml": &fstest.MapFile{Data: []byte(tc.yaml)}})
			if (err == nil) != tc.valid {
				t.Fatalf("settings: %+v %v", settings, err)
			}
			if tc.valid && (!settings.CollectorMonitors[0].IsEnabled() || settings.CollectorMonitors[0].AllowOperations || settings.CollectorMonitors[1].IsEnabled()) {
				t.Fatal("unsafe defaults")
			}
		})
	}
	var monitors []CollectorMonitor
	for index := 0; index < MaxAdditionalMonitors; index++ {
		monitors = append(monitors, CollectorMonitor{ID: fmt.Sprintf("monitor-%d", index)})
	}
	if err := ValidateCollectorMonitors(monitors); err != nil {
		t.Fatal(err)
	}
	if ValidateCollectorMonitors(append(monitors, CollectorMonitor{ID: "extra"})) == nil {
		t.Fatal("accepted more than eight total monitors")
	}
	schemas := compiledSchemas(t)
	for _, value := range []any{
		map[string]any{"collectorMonitors": []any{map[string]any{"id": "default"}}},
		map[string]any{"collectorMonitors": []any{map[string]any{"id": "../escape"}}},
	} {
		if schemas["settings.schema.json"].Validate(value) == nil {
			t.Fatalf("schema accepted: %v", value)
		}
	}
}
