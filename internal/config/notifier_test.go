package config

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

func TestToastConfigurationNeedsNoFeishuApplication(t *testing.T) {
	files := fstest.MapFS{"notifiers.yaml": {Data: []byte("notifiers: [{id: desktop, type: toast}]\n")}}
	cfg, err := Load(files)
	if err != nil || len(cfg.FeishuApps) != 0 || len(cfg.Notifiers) != 1 {
		t.Fatalf("toast configuration=%+v,%v", cfg, err)
	}
	data, err := json.Marshal(NotifiersFile{Notifiers: cfg.Notifiers})
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if err := compiledSchemas(t)["notifiers.schema.json"].Validate(document); err != nil {
		t.Fatalf("toast schema disagrees with configuration: %v", err)
	}
	for _, field := range []string{"app: fake", "chatId: fake", "chatName: fake", "template: standard"} {
		files["notifiers.yaml"].Data = []byte("notifiers: [{id: desktop, type: toast, " + field + "}]\n")
		if _, err := Load(files); err == nil || !strings.Contains(err.Error(), "Windows 通知不使用") {
			t.Fatalf("irrelevant field accepted: %s, %v", field, err)
		}
	}
}

func TestWebhookConfigurationReferencesAndCredentialUpdate(t *testing.T) {
	data := []byte("# keep document\nnotifiers:\n  # keep channel\n  - id: hook\n    type: feishu_webhook\n    urlRef: old-url # keep URL comment\n    secretRef: old-sign\n    template: compact # keep template comment\n")
	updated, err := WebhookConfig(data, "hook", "feishu_webhook", "new-url", nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(fstest.MapFS{"notifiers.yaml": {Data: updated}})
	if err != nil || len(cfg.FeishuApps) != 0 || len(cfg.Notifiers) != 1 || cfg.Notifiers[0].URLRef != "new-url" || cfg.Notifiers[0].SecretRef != "old-sign" || cfg.Notifiers[0].Template != "compact" {
		t.Fatalf("update=%+v,%v", cfg, err)
	}
	for _, comment := range []string{"keep document", "keep channel", "keep URL comment", "keep template comment"} {
		if !strings.Contains(string(updated), comment) {
			t.Fatalf("comment lost: %s", comment)
		}
	}
	empty := ""
	updated, err = WebhookConfig(updated, "hook", "feishu_webhook", "newer-url", &empty)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseNotifiers(updated)
	if err != nil || parsed.Notifiers[0].SecretRef != "" || strings.Contains(string(updated), "secretRef:") {
		t.Fatalf("signature not cleared: %s, %v", updated, err)
	}
	for _, raw := range []string{"", "notifiers: []\n", "notifiers: null\n"} {
		created, err := WebhookConfig([]byte(raw), "new-hook", "feishu_webhook", "new-url", nil)
		if err != nil {
			t.Fatalf("create in empty document: %q, %v", raw, err)
		}
		if _, err := Load(fstest.MapFS{"notifiers.yaml": {Data: created}}); err != nil {
			t.Fatalf("created document not valid: %s, %v", created, err)
		}
	}
}

func TestWebhookNotifierSchemaRejectsPlaintextAndIrrelevantFields(t *testing.T) {
	for _, value := range []string{
		`{id: hook, type: feishu_webhook}`,
		`{id: hook, type: feishu_webhook, urlRef: fake, secretRef: ''}`,
		`{id: hook, type: feishu_webhook, urlRef: 'https://example.invalid/fake'}`,
		`{id: hook, type: feishu_webhook, urlRef: fake, app: ''}`,
		`{id: hook, type: toast, urlRef: ''}`,
		`{id: hook, type: toast, app: ''}`,
	} {
		files := fstest.MapFS{"notifiers.yaml": {Data: []byte("notifiers: [" + value + "]\n")}}
		if _, err := Load(files); err == nil {
			t.Fatalf("invalid channel accepted: %s", value)
		}
	}
	files := fstest.MapFS{"notifiers.yaml": {Data: []byte("notifiers: [{id: hook, type: feishu_webhook, urlRef: hook-url, secretRef: hook-sign, template: standard}]\n")}}
	cfg, err := Load(files)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(NotifiersFile{Notifiers: cfg.Notifiers})
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if err := compiledSchemas(t)["notifiers.schema.json"].Validate(document); err != nil {
		t.Fatalf("Webhook schema and configuration disagree: %v", err)
	}
}
