package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

func TestServiceSwitchCLIUsesHTTPPreviewConfirmationAndVersions(t *testing.T) {
	for _, test := range []struct {
		name      string
		flags     []string
		input     string
		posts     int32
		wantError bool
	}{
		{"preview", []string{"--json"}, "", 0, false},
		{"json-without-confirmation", []string{"--json", "--apply"}, "", 0, true},
		{"json-apply", []string{"--json", "--apply", "--yes", "--refs", "", "--public-probes=false"}, "", 1, false},
		{"interactive-no", []string{"--apply"}, "n\n", 0, false},
		{"interactive-yes", []string{"--apply", "--remove-missing"}, "yes\n", 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var posts, previews, waits atomic.Int32
			sha, version := strings.Repeat("a", 64), strings.Repeat("b", 64)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fake-env-only-token" {
					t.Error("token missing")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/collectors":
					io.WriteString(w, `[{"id":"fake-host","hostname":"fake-pc"}]`)
				case "/api/v1/collectors/fake-host/service-switch":
					previews.Add(1)
					if r.Method != "GET" || len(r.URL.Query()) != 1 {
						t.Error("preview shape changed")
					}
					json.NewEncoder(w).Encode(config.ServiceSwitchPreview{Collector: config.ServiceSwitchCollector{ID: "fake-host", Hostname: "fake-pc"}, SHA256: sha, TargetsVersion: version, Items: []config.ServiceSwitchImportItem{{Ref: "app:fake", Name: "虚构应用", Action: "create", TargetID: "fake", Target: &config.Target{ID: "fake"}}}})
				case "/api/v1/collectors/fake-host/service-switch/import":
					posts.Add(1)
					var body map[string]json.RawMessage
					if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil || string(body["sha256"]) != `"`+sha+`"` || string(body["targetsVersion"]) != `"`+version+`"` {
						t.Error("preview versions missing")
					}
					if test.name == "json-apply" && (string(body["refs"]) != "[]" || string(body["publicProbes"]) != "false") {
						t.Error("empty refs/public flag changed")
					}
					io.WriteString(w, `{"created":["fake"],"updated":[],"removed":[],"targetsVersion":"`+version+`"}`)
				default:
					t.Error("unexpected endpoint " + r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			deps := defaultImportDependencies()
			deps.Token = func() string { return "fake-env-only-token" }
			deps.Wait = func(_ context.Context, duration time.Duration) error {
				if duration != 2*time.Second {
					t.Error(duration)
				}
				waits.Add(1)
				return nil
			}
			cmd := newImportCommand(deps)
			cmd.SetArgs(append([]string{"service-switch", "--collector", "FAKE-PC", "--url", server.URL}, test.flags...))
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(io.Discard)
			cmd.SetIn(strings.NewReader(test.input))
			err := cmd.Execute()
			if (err != nil) != test.wantError || posts.Load() != test.posts || waits.Load() != test.posts {
				t.Fatalf("posts=%d waits=%d output=%s err=%v", posts.Load(), waits.Load(), output.String(), err)
			}
			if test.wantError && previews.Load() != 0 {
				t.Fatal("unconfirmed JSON apply accessed API")
			}
			if strings.Contains(output.String(), "fake-env-only-token") {
				t.Fatal("token leaked")
			}
			if test.name == "preview" && !json.Valid(output.Bytes()) {
				t.Fatal(output.String())
			}
			if test.name == "interactive-no" && !strings.Contains(output.String(), "已取消") {
				t.Fatal(output.String())
			}
		})
	}
}
