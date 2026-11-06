package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestRuleControlsCLIUpdatesOnlyTheSpecifiedDatabase(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "data", "lgmon.db")
	db, err := store.Open(context.Background(), store.Options{Path: path, Now: func() time.Time { return time.UnixMilli(1000000) }})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	alert := rules.Alert{ID: "fake-cli-alert", RuleID: "down", CollectorID: "fake", TargetID: "web", State: "firing", StartedAt: 1000000}
	if err := db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, TS: 1000000, Kind: "firing"}); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := execute([]string{"alerts", "ack", alert.ID, "--home", home, "--json"}, &out, &stderr); code != 0 {
		t.Fatalf("ack CLI: %d %s", code, stderr.String())
	}
	var acknowledged map[string]any
	if json.Unmarshal(out.Bytes(), &acknowledged) != nil || acknowledged["ok"] != true {
		t.Fatalf("ack JSON: %s", out.String())
	}
	rows, err := db.ActiveAlerts(context.Background())
	if err != nil || len(rows) != 1 || rows[0].AcknowledgedAt == 0 {
		t.Fatalf("CLI did not persist ack: %+v %v", rows, err)
	}
	out.Reset()
	stderr.Reset()
	if code := execute([]string{"silence", "add", "--rule", "down", "--for", "2h", "--reason", "test maintenance", "--home", home, "--json"}, &out, &stderr); code != 0 {
		t.Fatalf("silence CLI: %d %s", code, stderr.String())
	}
	var silence store.Silence
	if json.Unmarshal(out.Bytes(), &silence) != nil || silence.RuleID != "down" || silence.EndsAt-silence.StartsAt != (2*time.Hour).Milliseconds() {
		t.Fatalf("silence JSON: %s", out.String())
	}
	for _, args := range [][]string{{"silence", "add", "--for", "2h", "--reason", "x"}, {"silence", "add", "--rule", "down", "--target", "web", "--for", "2h", "--reason", "x"}, {"silence", "add", "--target", "web", "--for", "0s", "--reason", "x"}, {"alerts", "ack", "missing"}} {
		out.Reset()
		stderr.Reset()
		args = append(args, "--home", home)
		if execute(args, &out, &stderr) == 0 {
			t.Fatalf("invalid control accepted: %v", args)
		}
	}
}

func TestRuleControlsMissingDataDoNotCreateHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "missing")
	var out, stderr bytes.Buffer
	if execute([]string{"alerts", "ack", "fake", "--home", home}, &out, &stderr) == 0 || !strings.Contains(stderr.String(), "尚无监控数据") {
		t.Fatalf("missing control data: %s", stderr.String())
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("control command created a missing home")
	}
}
