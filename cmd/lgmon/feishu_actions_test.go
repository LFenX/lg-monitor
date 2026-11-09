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

func TestFeishuCardActionsCLIAdministratorAndJSON(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "config")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "notifiers.yaml")
	original := []byte("# Keep this comment\nfeishuApps: [{id: fake-app, appId: cli_fake_actions, secretRef: fake-ref, domain: feishu}]\nnotifiers: [{id: fake-channel, type: feishu_app, app: fake-app, chatId: oc_fake_group}]\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(admin bool, operation string) (string, error) {
		cmd := newFeishuActionsCommand(func() (bool, error) { return admin, nil })
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		cmd.PersistentFlags().String("home", home, "")
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		cmd.SetArgs([]string{operation, "fake-channel", "--json"})
		err := cmd.ExecuteContext(context.Background())
		return output.String(), err
	}
	if _, err := run(false, "enable"); err == nil {
		t.Fatal("non-admin delegated actions")
	}
	data, _ := os.ReadFile(path)
	if !bytes.Equal(original, data) {
		t.Fatal("rejected CLI changed config")
	}
	for _, step := range []struct {
		op             string
		admin, enabled bool
	}{{"status", false, false}, {"enable", true, true}, {"status", false, true}, {"disable", true, false}} {
		out, err := run(step.admin, step.op)
		var status api.CardActionsStatus
		if err != nil || json.Unmarshal([]byte(out), &status) != nil || status.Enabled != step.enabled || status.Notifier != "fake-channel" {
			t.Fatal(step, out, err)
		}
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "# Keep this comment") {
		t.Fatal("CLI lost unrelated comments")
	}
	if _, err := os.Stat(filepath.Join(home, "data")); !os.IsNotExist(err) {
		t.Fatal("CLI created runtime database")
	}
}
