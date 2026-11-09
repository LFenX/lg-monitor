package config

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPersonalNotifierRequiresOneDestinationAndCannotGrantGroupActions(t *testing.T) {
	base := "feishuApps: [{id: fake-app, appId: cli_fake, secretRef: fake-ref, domain: feishu}]\nnotifiers:\n- {id: personal, type: feishu_app, app: fake-app, openId: ou_fake_person}\n"
	files := fstest.MapFS{"notifiers.yaml": {Data: []byte(base)}}
	cfg, err := Load(files)
	if err != nil || cfg.Notifiers[0].OpenID != "ou_fake_person" || cfg.Notifiers[0].ChatID != "" {
		t.Fatal(cfg, err)
	}
	encoded, _ := json.Marshal(NotifiersFile{FeishuApps: cfg.FeishuApps, Notifiers: cfg.Notifiers})
	var value any
	json.Unmarshal(encoded, &value)
	if err := compiledSchemas(t)["notifiers.schema.json"].Validate(value); err != nil {
		t.Fatal("schema rejected private receiver", err)
	}
	if _, err := CardActionsConfig([]byte(base), "personal", true); err == nil {
		t.Fatal("personal recipient granted group actions")
	}
	for _, replacement := range []string{
		"", "chatId: oc_fake, openId: ou_fake_person", "openId: bad", "openId: 'ou_fake person'", "openId: ou_fake_person, cardActions: true",
	} {
		files["notifiers.yaml"].Data = []byte(strings.Replace(base, "openId: ou_fake_person", replacement, 1))
		if _, err := Load(files); err == nil {
			t.Fatal("invalid recipient accepted", replacement)
		}
	}
	for _, kind := range []string{"toast", "webhook", "feishu_webhook"} {
		files["notifiers.yaml"].Data = []byte(strings.Replace(base, "type: feishu_app", "type: "+kind, 1))
		if _, err := Load(files); err == nil {
			t.Fatal("non-app receiver accepted open_id", kind)
		}
	}
}
