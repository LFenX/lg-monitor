package config

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestCacheMaxSizeUsesDecimalUnitsAndBounds(t *testing.T) {
	for _, test := range []struct {
		input string
		want  int64
	}{
		{"", 500000000}, {"64MB", 64000000}, {"500MB", 500000000}, {"10GB", 10000000000},
		{"500000000B", 500000000}, {"500000KB", 500000000},
	} {
		got, err := (CacheSettings{MaxSize: test.input}).MaxBytes()
		if err != nil || got != test.want {
			t.Fatalf("%q: %d %v", test.input, got, err)
		}
	}
	for _, input := range []string{"63MB", "10000000001B", "11GB", "1", "64MiB", "1.5GB", "-1MB", "18446744073709551616GB"} {
		if _, err := (CacheSettings{MaxSize: input}).MaxBytes(); err == nil {
			t.Fatalf("invalid cache size accepted: %q", input)
		}
	}
	files := fstest.MapFS{"settings.yaml": {Data: []byte("cache:\n  maxSize: 63MB\n")}}
	if _, err := LoadSettings(files); err == nil || !strings.Contains(err.Error(), "第 2 行") || !strings.Contains(err.Error(), "64MB～10GB") {
		t.Fatalf("cache size error omitted source line: %v", err)
	}
}
