package config

import (
	"strings"
	"testing"
	"testing/fstest"
)

const cardActionConfigFixture = `# Keep the application and destination unchanged.
feishuApps:
- id: fake-app
  appId: cli_fake_fixture
  secretRef: fake-secret
  domain: feishu
notifiers:
- &base
  id: first
  type: feishu_app
  app: fake-app
  chatId: oc_fake_destination
  cardActions: false # owner delegation
- <<: *base
  id: second
`

func TestCardActionDelegationPreservesMergedChannelsAndRejectsNonApplicationTypes(t *testing.T) {
	data, err := CardActionsConfig([]byte(cardActionConfigFixture), "first", true)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(fstest.MapFS{"notifiers.yaml": {Data: data}})
	if err != nil || len(cfg.Notifiers) != 2 || !cfg.Notifiers[0].CardActions || cfg.Notifiers[1].CardActions {
		t.Fatal("grant changed a merged destination", cfg, err)
	}
	if !strings.Contains(string(data), "owner delegation") || !strings.Contains(string(data), "# Keep") {
		t.Fatal("unrelated comments lost")
	}
	disabled, err := CardActionsConfig(data, "first", false)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ParseNotifiers(disabled)
	if err != nil || after.Notifiers[0].CardActions || after.Notifiers[1].CardActions {
		t.Fatal("revocation failed", err)
	}
	for _, kind := range []string{"toast", "webhook", "feishu_webhook"} {
		input := "notifiers:\n- id: bad\n  type: " + kind + "\n  cardActions: true\n"
		if _, err := Load(fstest.MapFS{"notifiers.yaml": {Data: []byte(input)}}); err == nil || !strings.Contains(err.Error(), "cardActions") {
			t.Fatal("invalid action type accepted or unlocated", kind, err)
		}
	}
}

func TestCardActionGrantGuardCoversCopiesIdentityAndRevocations(t *testing.T) {
	before := NotifiersFile{FeishuApps: []FeishuApp{{ID: "app", AppID: "cli_fake_fixture", Domain: "feishu"}}, Notifiers: []Notifier{{ID: "first", Type: "feishu_app", App: "app", ChatID: "oc_fake_first", CardActions: true}}}
	clone := func() NotifiersFile {
		return NotifiersFile{FeishuApps: append([]FeishuApp(nil), before.FeishuApps...), Notifiers: append([]Notifier(nil), before.Notifiers...)}
	}
	for _, field := range []string{"new", "chat", "app", "domain", "copy", "enable"} {
		after := clone()
		switch field {
		case "new":
			after.Notifiers[0].ID = "new-channel"
		case "chat":
			after.Notifiers[0].ChatID = "oc_fake_other"
		case "app":
			after.FeishuApps[0].AppID = "cli_fake_other"
		case "domain":
			after.FeishuApps[0].Domain = "lark"
		case "copy":
			other := after.Notifiers[0]
			other.ID = "copy"
			after.Notifiers = append(after.Notifiers, other)
		case "enable":
			beforeOff := clone()
			beforeOff.Notifiers[0].CardActions = false
			if !CardActionGrantsAdded(beforeOff, after) {
				t.Fatal("enable bypassed guard")
			}
			continue
		}
		if !CardActionGrantsAdded(before, after) {
			t.Fatal("grant change not detected", field)
		}
	}
	for _, field := range []string{"same", "rotation", "template", "revoke", "remove"} {
		after := clone()
		switch field {
		case "rotation":
			after.FeishuApps[0].SecretRef = "new-protected-reference"
		case "template":
			after.Notifiers[0].Template = "compact"
		case "revoke":
			after.Notifiers[0].CardActions = false
		case "remove":
			after.Notifiers = nil
		}
		if CardActionGrantsAdded(before, after) {
			t.Fatal("ordinary edit or revocation blocked", field)
		}
	}
}

func TestCardActionScalarAliasDoesNotDelegateAnotherChannel(t *testing.T) {
	text := strings.Replace(cardActionConfigFixture, "cardActions: false # owner delegation", "cardActions: &disabled false # owner delegation", 1)
	text = strings.Replace(text, "- <<: *base\n  id: second", "- <<: *base\n  id: second\n  cardActions: *disabled", 1)
	data, err := CardActionsConfig([]byte(text), "first", true)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(fstest.MapFS{"notifiers.yaml": {Data: data}})
	if err != nil || !cfg.Notifiers[0].CardActions || cfg.Notifiers[1].CardActions {
		t.Fatal("scalar alias expanded delegation", err)
	}
}
