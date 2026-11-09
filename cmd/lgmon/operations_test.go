package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/api"
)

func TestServiceOperationsCLILocalAuthorizationAndExplicitRemoteConfirmation(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "config")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "settings.yaml")
	original := []byte("# retain owner note\nhttpListen: 127.0.0.1:7444\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	factory := func(string) (api.MCPCaller, error) {
		return api.MCPCallerFunc(func(_ context.Context, op api.Operation, r api.Request) (api.MCPResponse, error) {
			calls++
			if op != api.RestartService || r.Params["id"] != "fake-host" {
				t.Fatal(op, r)
			}
			var input api.ServiceOperationInput
			if json.Unmarshal(r.Body, &input) != nil || !input.Confirmed || input.GrantID != "restart-demo" {
				t.Fatal(string(r.Body))
			}
			return api.MCPResponse{Status: 200, Body: json.RawMessage(`{"state":"succeeded","requestId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)}, nil
		}), nil
	}
	run := func(admin bool, args ...string) (string, error) {
		cmd := newOperationsCommand(func() (bool, error) { return admin, nil }, factory)
		cmd.PersistentFlags().String("home", home, "")
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		cmd.SetArgs(append(args, "--json"))
		err := cmd.ExecuteContext(context.Background())
		return output.String(), err
	}
	if out, err := run(false, "allow", "restart-demo", "--service", "FakeService"); err == nil || !json.Valid([]byte(out)) {
		t.Fatal(out, err)
	}
	data, _ := os.ReadFile(path)
	if !bytes.Equal(data, original) {
		t.Fatal("non-admin changed local grants")
	}
	for _, args := range [][]string{{"allow", "restart-demo", "--service", "FakeService"}, {"enable"}, {"status"}, {"revoke", "restart-demo"}} {
		if out, err := run(true, args...); err != nil || !json.Valid([]byte(out)) {
			t.Fatal(args, out, err)
		}
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "# retain owner note") || !strings.Contains(string(data), "127.0.0.1:7444") {
		t.Fatal("local authorization lost unrelated settings")
	}
	args := []string{"run", "restart-demo", "--collector", "fake-host", "--request-id", strings.Repeat("a", 32), "--version", strings.Repeat("b", 64), "--reason", "本人确认的虚构演示"}
	if out, err := run(true, args...); err == nil || calls != 0 {
		t.Fatal("missing confirmation called remote API", out, err, calls)
	}
	if out, err := run(true, append(args, "--confirm")...); err != nil || !json.Valid([]byte(out)) || calls != 1 {
		t.Fatal(out, err, calls)
	}
	if _, err := os.Stat(filepath.Join(home, "data")); !os.IsNotExist(err) {
		t.Fatal("CLI authorization created an execution database")
	}
}
