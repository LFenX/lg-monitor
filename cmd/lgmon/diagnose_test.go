package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestDiagnoseCLIOnlineOfflineJSONAndNoHomeCreation(t *testing.T) {
	home := t.TempDir()
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(home, "data", "lgmon.db"), DisableMaintenance: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	alert := rules.Alert{ID: "fake-cli-diagnostic", RuleID: "fake-rule", CollectorID: "fake-cli-host", TargetID: "web", State: rules.StateResolved, Severity: "warning", StartedAt: 1000, ResolvedAt: 2000, Summary: "fake-private-cli", InstanceKey: "fake-private-instance"}
	if err := db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, TS: 1000, Kind: "resolved"}); err != nil {
		t.Fatal(err)
	}
	for _, online := range []bool{true, false} {
		if !online {
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
		}
		var stdout, stderr bytes.Buffer
		code := execute([]string{"diagnose", alert.ID, "--home", home, "--json"}, &stdout, &stderr)
		var value store.DiagnosticPackage
		if code != 0 || stderr.Len() != 0 || json.Unmarshal(stdout.Bytes(), &value) != nil || value.Alert.ID != alert.ID || len(value.Coverage) != 6 || strings.Contains(stdout.String(), "fake-private") {
			t.Fatalf("diagnose online=%t: code=%d %s %s", online, code, stdout.String(), stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"diagnose", alert.ID, "--home", home}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "先核对 schemaVersion") || !strings.Contains(stdout.String(), `"schemaVersion": 1`) {
		t.Fatal("human handoff omitted prompt or JSON", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	absent := filepath.Join(t.TempDir(), "absent")
	code := execute([]string{"diagnose", "missing", "--home", absent, "--json"}, &stdout, &stderr)
	if code == 0 || !json.Valid(stdout.Bytes()) || !strings.Contains(stdout.String(), "diagnostic_failed") {
		t.Fatal("missing database reported success", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("read-only diagnostics created home", err)
	}
}
