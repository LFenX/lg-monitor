package config

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestRetentionDefaultsStrictValidationAndLastGood(t *testing.T) {
	defaults, err := (Retention{}).Effective()
	if err != nil || defaults.Raw != 24*time.Hour || defaults.Minute != 14*24*time.Hour || defaults.Hour != 365*24*time.Hour {
		t.Fatalf("retention defaults: %+v %v", defaults, err)
	}
	for _, text := range []string{"retention: {raw: 0s}", "retention: {raw: -1h}", "retention: {raw: 24}", "retention: {raw: 23h}", "retention: {raw: 48h, minute: 24h}", "retention: {minute: 48h, hour: 24h}", "retention: {typo: 1h}", "unknown: true"} {
		files := fstest.MapFS{"settings.yaml": {Data: []byte(text)}}
		if _, err := Load(files); err == nil || !strings.Contains(err.Error(), "settings.yaml 第") {
			t.Fatalf("invalid settings accepted or not located: %s %v", text, err)
		}
	}
	files := validFiles()
	files["settings.yaml"] = &fstest.MapFile{Data: []byte("retention: {raw: 48h, minute: 720h, hour: 17520h}\n")}
	loader := &memoryLoader{files: files}
	manager, err := NewManager(loader, nil)
	if err != nil {
		t.Fatal(err)
	}
	copy := manager.Snapshot()
	copy.Settings.Retention.Raw = "1h"
	if manager.Snapshot().Settings.Retention.Raw != "48h" {
		t.Fatal("settings snapshot aliases active policy")
	}
	loader.replace(t, "settings.yaml", "raw: 48h", "raw: 0s")
	if err := manager.Reload(); err == nil || manager.Snapshot().Settings.Retention.Raw != "48h" {
		t.Fatal("invalid settings replaced last good policy")
	}
	if !(&fileWatcher{directory: "config"}).relevant("config/settings.yaml") {
		t.Fatal("settings changes do not trigger reload")
	}
}

func TestDayDurationsAndEqualRetention(t *testing.T) {
	for text, want := range map[Duration]time.Duration{"14d": 14 * 24 * time.Hour, "1d2h30m": 26*time.Hour + 30*time.Minute, ".5d": 12 * time.Hour, "1.5d": 36 * time.Hour, "1.d": 24 * time.Hour, "0.000000000001d": 86 * time.Nanosecond} {
		if value, err := text.Parse(); err != nil || value != want {
			t.Fatalf("duration %s: %s want %s %v", text, value, want, err)
		}
	}
	for _, text := range []Duration{"1dd", "day", "1e3d", "999999999999999d"} {
		if _, err := text.Parse(); err == nil {
			t.Fatalf("invalid day duration accepted: %s", text)
		}
	}
	if _, err := (Retention{Raw: "14d", Minute: "14d", Hour: "14d"}).Effective(); err != nil {
		t.Fatal("equal retention rejected", err)
	}
	files := fstest.MapFS{"settings.yaml": {Data: []byte("retention:\n  raw: 10d\n  minute: 7d\n  hour: 365d\n")}}
	if _, err := Load(files); err == nil || !strings.Contains(err.Error(), "settings.yaml 第 3 行") || !strings.Contains(err.Error(), "7d") {
		t.Fatalf("retention ordering error not located: %v", err)
	}
}
