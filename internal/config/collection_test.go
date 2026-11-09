package config

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestCollectionIntervalsDefaultsConfigurationAndFieldLocations(t *testing.T) {
	defaults, err := (CollectionSettings{}).Effective()
	if err != nil || defaults != (CollectionIntervals{2 * time.Second, 15 * time.Second, 5 * time.Second, 10 * time.Second}) {
		t.Fatal(defaults, err)
	}
	valid := "collection:\n  targetInterval: 500ms\n  processInterval: 1s\n  systemInterval: 7s\n  serviceInterval: 1m\n"
	settings, err := LoadSettings(fstest.MapFS{"settings.yaml": &fstest.MapFile{Data: []byte(valid)}})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := settings.Collection.Effective()
	if err != nil || actual != (CollectionIntervals{500 * time.Millisecond, time.Second, 7 * time.Second, time.Minute}) {
		t.Fatal(actual, err)
	}
	for _, tc := range []struct{ field, value string }{
		{"targetInterval", "499ms"}, {"targetInterval", "5.001s"}, {"processInterval", "0s"}, {"processInterval", "61s"}, {"systemInterval", "-1s"}, {"serviceInterval", "1.0001s"}, {"serviceInterval", "invalid"},
	} {
		text := "collection:\n  " + tc.field + ": " + tc.value + "\n"
		_, err := LoadSettings(fstest.MapFS{"settings.yaml": &fstest.MapFile{Data: []byte(text)}})
		if err == nil || !strings.Contains(err.Error(), "settings.yaml 第 2 行") || !strings.Contains(err.Error(), tc.field) {
			t.Fatalf("%s: %v", text, err)
		}
	}
	for _, data := range []string{"collection: {unknown: 2s}", "collection: {targetInterval: 2}"} {
		if _, err := LoadSettings(fstest.MapFS{"settings.yaml": &fstest.MapFile{Data: []byte(data)}}); err == nil {
			t.Fatal("invalid sampling configuration accepted", data)
		}
	}
}
