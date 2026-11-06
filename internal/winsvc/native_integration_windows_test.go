//go:build integration && windows

package winsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeReadOnlySCMAndServiceLogAppend(t *testing.T) {
	controller, err := NativeController()
	if err != nil {
		t.Fatal(err)
	}
	status, err := controller.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Read-only lgmon service status: installed=%t state=%s", status.Installed, status.State)
	home := t.TempDir()
	for i := 0; i < 2; i++ {
		logger, file, err := openServiceLog(home, slog.LevelInfo)
		if err != nil {
			t.Fatal(err)
		}
		logger.Debug("must be filtered")
		logger.Info("own service log test", "iteration", i)
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(home, "logs", "lgmon.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("log append/filter lost records: %s", data)
	}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil || record["level"] != "INFO" || record["msg"] != "own service log test" {
			t.Fatalf("invalid JSON service log: %s %v", line, err)
		}
		if timestamp, ok := record["time"].(float64); !ok || timestamp <= 0 {
			t.Fatalf("service log time is not Unix milliseconds: %s", line)
		}
	}
}
