package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/config"
)

func TestStatusWarnsAboutUnencryptedHTTPWithoutPollutingJSON(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:7444", "127.0.0.1:7444", "100.126.79.86:7444", "[fd7a:115c:a1e0::1]:7444"} {
		t.Run(listen, func(t *testing.T) {
			home := t.TempDir()
			directory := filepath.Join(home, "config")
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(listen)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "settings.yaml"), append([]byte("httpListen: "), data...), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "rules.yaml"), []byte("broken: [\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, asJSON := range []bool{false, true} {
				cmd := newStatusCommand(openStatus)
				cmd.PersistentFlags().String("home", home, "test home")
				args := []string{}
				if asJSON {
					args = append(args, "--json")
				}
				cmd.SetArgs(args)
				var stdout, stderr bytes.Buffer
				cmd.SetOut(&stdout)
				cmd.SetErr(&stderr)
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				wantWarning := listen == "0.0.0.0:7444"
				if strings.Contains(stderr.String(), config.HTTPWarningText) != wantWarning {
					t.Fatalf("listen=%s warning=%q", listen, stderr.String())
				}
				if asJSON {
					var result statusResult
					if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
						t.Fatalf("diagnostic broke JSON: %s %v", stdout.String(), err)
					}
				}
			}
		})
	}
}

func TestStatusStillReadsDataWhenHTTPSettingsAreInvalid(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "config")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "settings.yaml"), []byte("httpListen: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newStatusCommand(openStatus)
	cmd.PersistentFlags().String("home", home, "test home")
	cmd.SetArgs([]string{"--json"})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result statusResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Available {
		t.Fatalf("status did not report the actual empty database: %s %v", stdout.String(), err)
	}
	if !strings.Contains(stderr.String(), "无法检查 HTTP") || !strings.Contains(stderr.String(), "settings.yaml") {
		t.Fatalf("settings failure was hidden: %q", stderr.String())
	}
}
