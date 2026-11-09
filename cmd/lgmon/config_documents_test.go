package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/spf13/cobra"
)

func TestConfigDocumentsCRUDUsesHTTPAndPreservesUnrelatedFields(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	t.Setenv("LGMON_TOKEN", "fake-config-token")
	document := json.RawMessage(`{"feishuApps":[{"id":"fake-app","appId":"cli_fake","secretRef":"fake-ref"}],"notifiers":[{"id":"first","type":"toast"},{"id":"second","type":"toast"}]}`)
	reads, writes := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/config/notifiers" || r.Header.Get("Authorization") != "Bearer fake-config-token" || r.URL.RawQuery != "" {
			t.Error("wrong route or token placement", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "GET":
			reads++
		case "PUT":
			writes++
			if r.Header.Get("Content-Type") != "application/json" {
				t.Error("missing JSON content type")
			}
			data, err := io.ReadAll(r.Body)
			if err != nil || !json.Valid(data) {
				t.Error("invalid request body", err)
				w.WriteHeader(500)
				return
			}
			document = data
		default:
			t.Error("unexpected HTTP method", r.Method)
		}
		w.Write(document)
	}))
	defer server.Close()
	run := func(want int, stdin string, args ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		root := newRootCommand()
		root.SetArgs(append(append([]string{"config"}, args...), "--url", server.URL, "--json"))
		root.SetIn(strings.NewReader(stdin))
		root.SetOut(&out)
		root.SetErr(&errOut)
		err := root.Execute()
		if (err != nil) != (want != 0) {
			t.Fatalf("args=%v err=%v stdout=%s stderr=%s", args, err, out.String(), errOut.String())
		}
		if want == 0 && !json.Valid(out.Bytes()) {
			t.Fatal("non-JSON success", out.String())
		}
		return out.String()
	}
	run(0, "", "get", "notifiers")
	removed := run(0, "", "delete", "notifiers", "first")
	if strings.Contains(removed, `"first"`) || !strings.Contains(removed, `"second"`) || !strings.Contains(removed, `"secretRef":"fake-ref"`) {
		t.Fatal("delete lost or exposed unrelated configuration", removed)
	}
	run(1, "", "delete", "notifiers", "missing")
	if writes != 1 {
		t.Fatal("missing ID caused a write", writes)
	}
	run(0, `{"notifiers":[]}`, "put", "notifiers", "--file", "-")
	if reads != 3 || writes != 2 || string(document) != `{"notifiers":[]}` {
		t.Fatal("wrong read/replace semantics", reads, writes, string(document))
	}
	for _, input := range []string{"invalid", "[]", strings.Repeat(" ", api.MaxRequestBody+1)} {
		run(1, input, "put", "notifiers", "--file", "-")
	}
	if writes != 2 {
		t.Fatal("invalid/oversize input reached monitor")
	}
}

func TestConfigDocumentErrorsKeepAPIIssuesAndExitFailure(t *testing.T) {
	root := &cobra.Command{Use: "test", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newConfigDocumentCommands(func(string) (api.MCPCaller, error) {
		return api.MCPCallerFunc(func(_ context.Context, _ api.Operation, _ api.Request) (api.MCPResponse, error) {
			return api.MCPResponse{Status: 422, Body: json.RawMessage(`{"error":{"code":"invalid_config","message":"仍被引用","issues":[{"file":"rules.yaml","line":3}]}}`)}, nil
		}), nil
	})...)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetIn(strings.NewReader(`{"targets":[]}`))
	root.SetArgs([]string{"put", "targets", "--file", "-", "--json"})
	if err := root.Execute(); err == nil || !json.Valid(out.Bytes()) || !strings.Contains(out.String(), `"line":3`) {
		t.Fatal("API rejection lost validation detail or failure", err, out.String())
	}
	for _, kind := range []string{"../targets", "unknown"} {
		if _, err := configDocumentOperation(kind, true); err == nil {
			t.Fatalf("accepted invalid kind %q", kind)
		}
	}
}
