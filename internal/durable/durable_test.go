// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package durable

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileReplacesAndLeavesNoTemporaryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "state.json")
	for _, value := range []string{"first", "second, longer"} {
		if err := WriteFile(path, []byte(value)); err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != value {
			t.Fatalf("got %q, %v", got, err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected only the state file, got %v (%v)", entries, err)
	}
}
