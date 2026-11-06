package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/config"
)

func TestConfigMergePreservesOtherAppsAndComments(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config", "notifiers.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	before := "# keep this comment\nfeishuApps:\n  - id: other\n    appId: cli_fake_other\n    secretRef: fake-other\n    domain: feishu\nnotifiers:\n  - id: other-channel\n    type: feishu_app\n    app: other\n    chatId: oc_fake_other\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	files := FileConfigs{Home: home}
	notifier := config.Notifier{ID: "feishu-alerts", Type: "feishu_app", App: "default", ChatID: "oc_fake_group", Template: "standard"}
	for i := 0; i < 2; i++ {
		if err := files.SaveNotifier(context.Background(), fakeApp, notifier); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := files.Read()
	if err != nil || len(cfg.FeishuApps) != 2 || len(cfg.Notifiers) != 2 || cfg.Notifiers[0].ID != "other-channel" {
		t.Fatalf("merge lost or duplicated entries: %+v %v", cfg, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(after), "keep this comment") || strings.Contains(string(after), "fake-private-secret") {
		t.Fatal("merge lost comments or wrote secret material")
	}
	if err := os.WriteFile(path, []byte("unknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := files.SaveNotifier(context.Background(), fakeApp, notifier); err == nil {
		t.Fatal("unknown configuration was silently overwritten")
	}
	after, _ = os.ReadFile(path)
	if string(after) != "unknown: true\n" {
		t.Fatal("invalid original file was modified")
	}
}

func TestCheckpointRoundTripCancellationAndCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "checkpoint.json")
	file := FileCheckpoints{Path: path}
	state, err := file.Load(context.Background())
	if err != nil || state.Version != 1 {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("read-only loading created the data directory")
	}
	state.CredentialsOK, state.App = true, fakeApp
	if err := file.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := file.Save(ctx, State{}); err == nil {
		t.Fatal("cancelled write succeeded")
	}
	got, err := file.Load(context.Background())
	if err != nil || got != state {
		t.Fatalf("checkpoint changed: %+v %v", got, err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"unexpected":"fake-private-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Load(context.Background()); err == nil || strings.Contains(err.Error(), "fake-private-secret") {
		t.Fatalf("corrupt state was accepted or exposed: %v", err)
	}
}
