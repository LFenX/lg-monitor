package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/config"
)

type fakeSecrets struct {
	plain []byte
	name  string
	value []byte
}

func (f *fakeSecrets) Get(context.Context, string) ([]byte, error) {
	f.plain = []byte("fake-acceptance-secret")
	return f.plain, nil
}

func (f *fakeSecrets) Set(_ context.Context, name string, plain []byte) error {
	f.name, f.value = name, bytes.Clone(plain)
	return nil
}

func sourceConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	data := "feishuApps:\n- id: demo\n  appId: cli_fake_acceptance\n  secretRef: fake-key\n  domain: feishu\nnotifiers:\n- id: test-channel\n  type: feishu_app\n  app: demo\n  chatId: oc_fake_acceptance\n  chatName: 虚构测试群\n  template: custom-not-copied\n- id: unused-channel\n  type: feishu_app\n  app: demo\n  chatId: oc_fake_unused\n"
	if err := os.WriteFile(filepath.Join(home, "config", "notifiers.yaml"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestPrepareIsolatesSelectedChannelAndClearsSecret(t *testing.T) {
	source := sourceConfig(t)
	before, _ := os.ReadFile(filepath.Join(source, "config", "notifiers.yaml"))
	opts := prepareOptions{SourceHome: source, Home: filepath.Join(t.TempDir(), "new home"), Notifier: "test-channel", Demo: `C:\temp\lgmon-m0-demo.exe`, Nonce: strings.Repeat("a", 32), Port: 23456}
	reader, writer := &fakeSecrets{}, &fakeSecrets{}
	result, err := prepare(context.Background(), opts, reader, writer)
	if err != nil || result.ChatName != "虚构测试群" || writer.name != "fake-key" || string(writer.value) != "fake-acceptance-secret" {
		t.Fatalf("prepare: %+v %v %+v", result, err, writer)
	}
	if !bytes.Equal(reader.plain, make([]byte, len(reader.plain))) {
		t.Fatal("decrypted secret was not cleared")
	}
	cfg, err := config.Load(os.DirFS(filepath.Join(opts.Home, "config")))
	if err != nil || len(cfg.Notifiers) != 1 || len(cfg.FeishuApps) != 1 || cfg.Notifiers[0].ID != "test-channel" || cfg.Notifiers[0].Template != "detailed" || cfg.Rules[0].For != "5s" || !strings.Contains(cfg.Targets[0].Match.Cmdline, opts.Nonce) {
		t.Fatalf("isolated config: %+v %v", cfg, err)
	}
	if _, err := prepare(context.Background(), opts, reader, writer); err == nil {
		t.Fatal("existing destination was overwritten")
	}
	after, _ := os.ReadFile(filepath.Join(source, "config", "notifiers.yaml"))
	if !bytes.Equal(before, after) {
		t.Fatal("source configuration changed")
	}
	entries, _ := os.ReadDir(source)
	if len(entries) != 1 {
		t.Fatal("source inspect/prepare wrote a database or secret")
	}
}

func TestInspectDoesNotExposeIdentifiersOrCreateData(t *testing.T) {
	home := sourceConfig(t)
	var output bytes.Buffer
	if err := execute(context.Background(), []string{"inspect", "--home", home, "--notifier", "test-channel"}, &output); err != nil {
		t.Fatal(err)
	}
	var result selection
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Notifier != "test-channel" {
		t.Fatalf("inspect: %s %v", output.String(), err)
	}
	if strings.Contains(output.String(), "cli_fake") || strings.Contains(output.String(), "oc_fake") {
		t.Fatal("inspect leaked application/chat identifiers")
	}
	if err := execute(context.Background(), []string{"inspect", "--home", home, "--notifier", "missing"}, &output); err == nil {
		t.Fatal("missing channel was accepted")
	}
	if _, err := os.Stat(filepath.Join(home, "data")); !os.IsNotExist(err) {
		t.Fatal("inspect created data directory")
	}
}
