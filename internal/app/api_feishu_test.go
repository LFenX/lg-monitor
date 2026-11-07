package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
)

func feishuBody(t *testing.T, value any) string {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestFeishuUnsavedChatDraftDoesNotActivateOrExposeReceipt(t *testing.T) {
	f := newAPIFixture(t)
	before := bytes.Clone(f.files.MapFS["notifiers.yaml"].Data)
	draft := config.Template{ID: "unsaved", Extends: "standard", Title: "尚未保存的模板 {{.Alert.State}}", Fields: []string{}}
	result := f.request(t, "POST", "/api/v1/feishu/test", "config", feishuBody(t, api.FeishuTestInput{ChatID: "oc_fake_unsaved", State: "resolved", Draft: &draft}), 200)
	if strings.Contains(result.Body.String(), "PRIVATE_RESPONSE") || !strings.Contains(result.Body.String(), `"ok":true`) {
		t.Fatalf("receipt leaked or test failed: %s", result.Body.String())
	}
	if len(f.client.sent) != 1 {
		t.Fatalf("sent=%d", len(f.client.sent))
	}
	card := f.client.sent[0]
	data := feishuBody(t, card)
	if card.ChatID != "oc_fake_unsaved" || card.State != "resolved" || !strings.Contains(data, "尚未保存的模板 resolved") || !strings.Contains(data, "这是测试消息") || strings.Contains(data, "column_set") {
		t.Fatalf("wrong draft/state/test label: %s", data)
	}
	if f.files.writes != 0 || !bytes.Equal(before, f.files.MapFS["notifiers.yaml"].Data) || len(f.manager.Snapshot().Templates) != 0 {
		t.Fatal("test activated a channel or saved a draft")
	}
}

func TestFeishuTestRejectsAmbiguousAndInvalidDraftWithoutFallbackOrQuota(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"both", `{"chatId":"oc_fake_api","template":"standard","draft":{"id":"draft","extends":"standard"}}`, 400},
		{"empty template still present", `{"chatId":"oc_fake_api","template":"","draft":{"id":"draft","extends":"standard"}}`, 400},
		{"null draft", `{"chatId":"oc_fake_api","draft":null}`, 400},
		{"unsafe AST", `{"chatId":"oc_fake_api","draft":{"id":"draft","extends":"standard","title":"{{range 100000000000}}{{end}}"}}`, 422},
		{"missing variable", `{"chatId":"oc_fake_api","draft":{"id":"draft","extends":"standard","title":"{{.DoesNotExist}}"}}`, 400},
		{"bad state", `{"chatId":"oc_fake_api","state":"digest"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAPIFixture(t)
			response := f.request(t, "POST", "/api/v1/feishu/test", "config", tc.body, tc.status)
			if len(f.client.sent) != 0 || f.files.writes != 0 {
				t.Fatal("invalid test sent or persisted a fallback")
			}
			if tc.name == "unsafe AST" && !strings.Contains(response.Body.String(), `"issues"`) {
				t.Fatal("missing field/line issues")
			}
			f.request(t, "POST", "/api/v1/feishu/test", "config", `{"chatId":"oc_fake_api"}`, 200)
			if len(f.client.sent) != 1 {
				t.Fatal("invalid draft consumed the rate limit")
			}
		})
	}
}

func TestFeishuTestDestinationRateLimitSharedAcrossRoutesAndAliases(t *testing.T) {
	for _, firstSaved := range []bool{false, true} {
		t.Run(map[bool]string{false: "chat first", true: "saved first"}[firstSaved], func(t *testing.T) {
			f := newAPIFixture(t)
			data := string(f.files.MapFS["notifiers.yaml"].Data)
			data = strings.Replace(data, "notifiers: [", "notifiers: [{id: alias, type: feishu_app, app: fake-app, chatId: oc_fake_api}, ", 1)
			f.files.MapFS["notifiers.yaml"] = &fstest.MapFile{Data: []byte(data)}
			if err := f.manager.Reload(); err != nil {
				t.Fatal(err)
			}
			path, body := "/api/v1/feishu/test", `{"chatId":"oc_fake_api"}`
			if firstSaved {
				path, body = "/api/v1/notifiers/fake-channel/test", "{}"
			}
			f.request(t, "POST", path, "config", body, 200)
			for _, saved := range []string{"fake-channel", "alias"} {
				f.request(t, "POST", "/api/v1/notifiers/"+saved+"/test", "config", "{}", 429)
			}
			response := f.request(t, "POST", "/api/v1/feishu/test", "config", `{"chatId":"oc_fake_api"}`, 429)
			if response.Header().Get("Retry-After") != "10" {
				t.Fatal("missing retry guidance")
			}
			f.request(t, "POST", "/api/v1/feishu/test", "config", `{"chatId":"oc_fake_other"}`, 200)
			f.now = f.now.Add(10 * time.Second)
			f.request(t, "POST", "/api/v1/notifiers/alias/test", "config", "{}", 200)
			if len(f.client.sent) != 3 {
				t.Fatalf("sent=%d", len(f.client.sent))
			}
		})
	}
}

func TestNotifierDraftOnlySupportedForCardChannels(t *testing.T) {
	f := newAPIFixture(t)
	secret := privateHookSecret
	f.request(t, "POST", "/api/v1/notifiers/hook/credentials", "admin", hookCredentialsBody(t, privateHookURL, &secret), 200)
	draft := config.Template{ID: "webhook-draft", Extends: "compact", Title: "Webhook 未保存模板"}
	body := feishuBody(t, api.NotifierTestInput{Draft: &draft})
	f.request(t, "POST", "/api/v1/notifiers/hook/test", "config", body, 200)
	if len(f.hook.sent) != 1 || !strings.Contains(feishuBody(t, f.hook.sent[0]), draft.Title) || len(f.manager.Snapshot().Templates) != 0 {
		t.Fatal("webhook draft was not used ephemerally")
	}
	// The ordinary Webhook and toast have no card-template contract.
	data := strings.TrimSpace(string(f.files.MapFS["notifiers.yaml"].Data)) + "\n"
	parsed, err := config.ParseNotifiers([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	parsed.Notifiers = append(parsed.Notifiers, config.Notifier{ID: "desktop", Type: "toast"})
	raw, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	f.files.MapFS["notifiers.yaml"] = &fstest.MapFile{Data: raw}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", "/api/v1/notifiers/desktop/test", "config", body, 400)
	if len(f.toast.values) != 0 {
		t.Fatal("unsupported draft displayed toast")
	}
	empty := ""
	f.request(t, "POST", "/api/v1/notifiers/plain/credentials", "admin", feishuBody(t, api.NotifierCredentialsInput{Type: "webhook", URL: "https://example.invalid/fake", SigningSecret: &empty}), 200)
	f.request(t, "POST", "/api/v1/notifiers/plain/test", "config", body, 400)
}

func TestFeishuSaveMergesChannelRulesAndPreservesComments(t *testing.T) {
	f := newAPIFixture(t)
	f.client.chats = func(context.Context) ([]feishu.Chat, error) {
		return []feishu.Chat{{ID: "oc_fake_new", Name: "旧群名"}}, nil
	}
	f.client.chatInfo = func(context.Context, string) (feishu.Chat, error) {
		return feishu.Chat{Name: "最新虚构群名"}, nil
	}
	f.files.MapFS["rules.yaml"] = &fstest.MapFile{Data: []byte("# rules document\nrules:\n  - id: down # rule id\n    type: process_absent\n    target: web\n    for: 0s\n    severity: critical\n    notify: [fake-channel] # recipients\n")}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	body := `{"chatId":"oc_fake_new","template":"compact","filter":{"severity":["critical"]},"rules":["down","down"]}`
	f.request(t, "POST", "/api/v1/feishu/notifiers", "config", body, 200)
	cfg := f.manager.Snapshot()
	if len(cfg.Notifiers) != 2 {
		t.Fatalf("channels=%+v", cfg.Notifiers)
	}
	saved := cfg.Notifiers[1]
	if saved.ID != "feishu-fake-app" || saved.ChatName != "最新虚构群名" || saved.Template != "compact" || saved.Filter == nil {
		t.Fatalf("saved=%+v", saved)
	}
	if !slices.Equal(cfg.Rules[0].Notify, []string{"fake-channel", saved.ID}) {
		t.Fatalf("rules=%+v", cfg.Rules)
	}
	for _, value := range []string{"# preserve this comment", "# rules document", "# rule id", "# recipients"} {
		all := string(f.files.MapFS["notifiers.yaml"].Data) + string(f.files.MapFS["rules.yaml"].Data)
		if !strings.Contains(all, value) {
			t.Fatalf("lost comment %s", value)
		}
	}
	// Omitted settings are retained; repeated rule selection does not duplicate recipients.
	f.request(t, "POST", "/api/v1/feishu/notifiers", "config", `{"chatId":"oc_fake_new","rules":["down"]}`, 200)
	cfg = f.manager.Snapshot()
	if len(cfg.Notifiers) != 2 || cfg.Notifiers[1].ID != saved.ID || cfg.Notifiers[1].Template != "compact" || cfg.Notifiers[1].Filter == nil || len(cfg.Rules[0].Notify) != 2 {
		t.Fatalf("non-idempotent save: %+v", cfg)
	}
	// An explicitly empty filter selects all severities without inventing null semantics.
	f.request(t, "POST", "/api/v1/feishu/notifiers", "config", `{"chatId":"oc_fake_new","filter":{}}`, 200)
	if len(f.manager.Snapshot().Notifiers[1].Filter.Severity) != 0 {
		t.Fatal("explicit all filter did not reset")
	}
	if len(f.client.sent) != 0 {
		t.Fatal("saving sent an unrequested test")
	}
}

func TestFeishuDraftOutputLimitAndSizeDegradationAreTruthful(t *testing.T) {
	f := newAPIFixture(t)
	draft := config.Template{ID: "huge-draft", Extends: "standard", Title: strings.Repeat("x", 65537)}
	f.request(t, "POST", "/api/v1/feishu/test", "config", feishuBody(t, api.FeishuTestInput{ChatID: "oc_fake_api", Draft: &draft}), 400)
	if len(f.client.sent) != 0 {
		t.Fatal("output overflow sent a fallback")
	}
	draft.Title = strings.Repeat("x", 30000)
	response := f.request(t, "POST", "/api/v1/feishu/test", "config", feishuBody(t, api.FeishuTestInput{ChatID: "oc_fake_api", Draft: &draft}), 200)
	if !strings.Contains(response.Body.String(), "已使用简洁模板") || len(f.client.sent) != 1 || !strings.Contains(string(f.client.sent[0].Card), "这是测试消息") {
		t.Fatalf("size degradation not disclosed: %s", response.Body.String())
	}
}

func TestFeishuSaveCollisionMissingGroupAndPartialRuleFailure(t *testing.T) {
	f := newAPIFixture(t)
	raw := strings.Replace(string(f.files.MapFS["notifiers.yaml"].Data), "notifiers: [", "notifiers: [{id: feishu-fake-app, type: toast}, ", 1)
	f.files.MapFS["notifiers.yaml"] = &fstest.MapFile{Data: []byte(raw)}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(f.files.MapFS["notifiers.yaml"].Data)
	f.request(t, "POST", "/api/v1/feishu/notifiers", "config", `{"chatId":"oc_fake_departed","rules":["down"]}`, 409)
	if !bytes.Equal(before, f.files.MapFS["notifiers.yaml"].Data) {
		t.Fatal("missing group changed channel")
	}
	f.client.chats = func(context.Context) ([]feishu.Chat, error) {
		return []feishu.Chat{{ID: "oc_fake_new", Name: "虚构新群"}}, nil
	}
	f.files.failure = errors.New("fake rules disk failure")
	f.files.failName = "rules.yaml"
	rulesBefore := bytes.Clone(f.files.MapFS["rules.yaml"].Data)
	response := f.request(t, "POST", "/api/v1/feishu/notifiers", "config", `{"chatId":"oc_fake_new","rules":["down"]}`, 200)
	if !strings.Contains(response.Body.String(), `"ok":false`) || !strings.Contains(response.Body.String(), "群已保存，但没能加进规则") {
		t.Fatalf("partial save misreported: %s", response.Body.String())
	}
	cfg := f.manager.Snapshot()
	if !slices.ContainsFunc(cfg.Notifiers, func(n config.Notifier) bool { return n.ID == "feishu-fake-app-2" }) || !bytes.Equal(rulesBefore, f.files.MapFS["rules.yaml"].Data) {
		t.Fatal("partial save did not retain the channel and original rules")
	}
}

func TestFeishuSaveValidationAndWriteFailureNeverPublishPartialNotifier(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		fail       bool
		status     int
	}{
		{"bad template", `{"chatId":"oc_fake_api","template":"missing"}`, false, 422},
		{"invalid rule", `{"chatId":"oc_fake_api","rules":["missing"]}`, false, 400},
		{"null filter", `{"chatId":"oc_fake_api","filter":null}`, false, 400},
		{"disk failure", `{"chatId":"oc_fake_api","template":"compact"}`, true, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAPIFixture(t)
			before := bytes.Clone(f.files.MapFS["notifiers.yaml"].Data)
			if tc.fail {
				f.files.failure = errors.New("fake notifier disk failure")
			}
			f.request(t, "POST", "/api/v1/feishu/notifiers", "config", tc.body, tc.status)
			if !bytes.Equal(before, f.files.MapFS["notifiers.yaml"].Data) || f.manager.Snapshot().Notifiers[0].Template != "" {
				t.Fatal("failed save changed configuration")
			}
		})
	}
}
