// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package update

import "testing"

func TestNewerComparesReleaseNumbers(t *testing.T) {
	for _, item := range []struct {
		a, b string
		want bool
	}{
		{"0.9.0", "0.8.3", true},
		{"v1.0.0", "0.99.0", true},
		{"0.10.0", "0.9.0", true},
		{"0.9.0", "0.9.0", false},
		{"0.9", "0.9.0", false},
		{"0.9.1", "0.9", true},
		{"0.8.3", "0.9.0", false},
		{"1.0.0-rc1", "0.9.0", true},
		{"dev", "0.9.0", false},
		{"0.9.0", "dev", false},
		{"", "0.1.0", false},
	} {
		if got := Newer(item.a, item.b); got != item.want {
			t.Errorf("Newer(%q, %q) = %v", item.a, item.b, got)
		}
	}
}
