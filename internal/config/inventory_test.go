package config

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestInventorySamplingSettingsValidateWithoutChangingM1Defaults(t *testing.T) {
	if value, err := (InventorySettings{}).EffectiveInterval(); err != nil || value != time.Minute {
		t.Fatal(value, err)
	}
	for _, value := range []string{"10s", "1m", "1h"} {
		cfg, err := Load(fstest.MapFS{"settings.yaml": {Data: []byte("inventory:\n  interval: " + value + "\n")}})
		if err != nil || cfg.Settings.Inventory.Interval != Duration(value) {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"0s", "9s", "61m", "invalid"} {
		_, err := Load(fstest.MapFS{"settings.yaml": {Data: []byte("inventory:\n  interval: " + value + "\n")}})
		if err == nil || !strings.Contains(err.Error(), "settings.yaml 第 2 行") {
			t.Fatal("missing located error", value, err)
		}
	}
}
