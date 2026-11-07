package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/notify"
)

func genericCredentials(t *testing.T, endpoint string, key *string) string {
	t.Helper()
	body, err := json.Marshal(api.NotifierCredentialsInput{Type: "webhook", URL: endpoint, SigningSecret: key})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestGenericWebhookCredentialsGenerateOncePreserveDisableAndHide(t *testing.T) {
	f := newAPIFixture(t)
	path := "/api/v1/notifiers/json-hook/credentials"
	endpoint := "https://receiver.example.test/FAKE_PRIVATE_ENDPOINT"
	body := genericCredentials(t, endpoint, nil)
	f.request(t, "POST", path, "config", body, 403)
	response := f.request(t, "POST", path, "admin", body, 200)
	var result api.NotifierCredentialsResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	key, err := notify.DecodeWebhookSecret([]byte(result.SigningSecret))
	defer clear(key)
	if err != nil || len(key) != 32 || !result.OK {
		t.Fatal("admin did not receive a new 32-byte key")
	}
	first := hookChannel(t, f.manager.Snapshot(), "json-hook")
	if string(f.vault.values[first.SecretRef]) != result.SigningSecret || string(f.vault.values[first.URLRef]) != endpoint {
		t.Fatal("generated credentials not stored")
	}
	secondResponse := f.request(t, "POST", path, "admin", body, 200)
	second := hookChannel(t, f.manager.Snapshot(), "json-hook")
	if second.SecretRef != first.SecretRef || strings.Contains(secondResponse.Body.String(), "signingSecret") {
		t.Fatal("omitted update changed or returned existing secret")
	}
	visible := secondResponse.Body.String() + f.request(t, "GET", "/api/v1/config/notifiers", "read", "", 200).Body.String() + string(f.files.MapFS["notifiers.yaml"].Data) + f.logs.String()
	if strings.Contains(visible, result.SigningSecret) || strings.Contains(visible, endpoint) {
		t.Fatal("credentials leaked after initial generated-key response")
	}
	empty := ""
	f.request(t, "POST", path, "admin", genericCredentials(t, endpoint, &empty), 200)
	if hookChannel(t, f.manager.Snapshot(), "json-hook").SecretRef != "" {
		t.Fatal("explicit empty key did not disable signing")
	}
	response = f.request(t, "POST", path, "admin", body, 200)
	if hookChannel(t, f.manager.Snapshot(), "json-hook").SecretRef != "" || strings.Contains(response.Body.String(), "signingSecret") {
		t.Fatal("omitted update re-enabled disabled signing")
	}
	if f.hookFactories != 0 || len(f.client.sent) != 0 {
		t.Fatal("saving credentials contacted a provider")
	}
	before := f.vault.setCalls
	invalid := "FAKE_PRIVATE_BAD_KEY"
	response = f.request(t, "POST", path, "admin", genericCredentials(t, endpoint, &invalid), 400)
	if f.vault.setCalls != before || strings.Contains(response.Body.String(), invalid) {
		t.Fatal("invalid key stored or echoed")
	}
	beforeChannel := hookChannel(t, f.manager.Snapshot(), "json-hook")
	f.files.failure = fmt.Errorf("FAKE_PRIVATE_WRITE_FAILURE")
	f.request(t, "POST", path, "admin", body, 500)
	if got := hookChannel(t, f.manager.Snapshot(), "json-hook"); got.URLRef != beforeChannel.URLRef || got.SecretRef != beforeChannel.SecretRef {
		t.Fatal("failed document write changed live credential references")
	}
}

func TestGenericWebhookAPITestBypassesFilterAuditsHTTPAndHasUniqueID(t *testing.T) {
	f := newAPIFixture(t)
	var payloads []config.WebhookPayload
	var ids []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload config.WebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		payloads = append(payloads, payload)
		ids = append(ids, r.Header.Get("webhook-id"))
		w.WriteHeader(202)
		fmt.Fprint(w, "FAKE_PRIVATE_RESPONSE")
	}))
	defer server.Close()
	f.request(t, "POST", "/api/v1/notifiers/json-hook/credentials", "admin", genericCredentials(t, server.URL, nil), 200)
	cfg := f.manager.Snapshot()
	for i := range cfg.Notifiers {
		if cfg.Notifiers[i].ID == "json-hook" {
			cfg.Notifiers[i].Filter = &config.NotifierFilter{Severity: []string{"info"}, Tags: []string{"unmatched"}, Collectors: []string{"unknown"}}
		}
	}
	body, _ := json.Marshal(config.NotifiersFile{FeishuApps: cfg.FeishuApps, Notifiers: cfg.Notifiers})
	f.request(t, "PUT", "/api/v1/config/notifiers", "config", string(body), 200)
	f.jsonFactory = func(endpoint, key []byte) (notify.JSONWebhookClient, error) {
		return notify.NewJSONWebhookClient(endpoint, key, notify.JSONWebhookOptions{Now: func() time.Time { return f.now }, Version: "fake-version"})
	}
	for i := 0; i < 2; i++ {
		response := f.request(t, "POST", "/api/v1/notifiers/json-hook/test", "config", `{}`, 200)
		if !strings.Contains(response.Body.String(), `"ok":true`) || strings.Contains(response.Body.String(), "FAKE_PRIVATE_RESPONSE") {
			t.Fatal(response.Body.String())
		}
		f.now = f.now.Add(11 * time.Second)
	}
	if len(payloads) != 2 || payloads[0].Type != "alert.test" || !strings.Contains(payloads[0].Data.Alert.Summary, "这是测试消息") || ids[0] == ids[1] {
		t.Fatalf("test bypass/identity failed: %v", ids)
	}
	result, err := f.sqlView.Query(context.Background(), "SELECT ok,status_code,message_id,error FROM notifications WHERE notifier_id='json-hook'")
	if err != nil || len(result.Rows) != 2 {
		t.Fatalf("HTTP audit missing: %+v %v", result, err)
	}
	encoded, _ := json.Marshal(result)
	if !bytes.Contains(encoded, []byte("202")) || bytes.Contains(encoded, []byte("FAKE_PRIVATE_RESPONSE")) {
		t.Fatalf("unsafe audit: %s", encoded)
	}
	for _, buffer := range f.vault.readBuffers {
		if !bytes.Equal(buffer, make([]byte, len(buffer))) {
			t.Fatal("private credential read buffer retained")
		}
	}
}
