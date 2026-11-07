package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify"
)

type testWebhookClient struct {
	sent   []feishu.CardMessage
	err    error
	closed int
}

func (c *testWebhookClient) SendCard(_ context.Context, value feishu.CardMessage) (feishu.SendResult, error) {
	c.sent = append(c.sent, value)
	return feishu.SendResult{}, c.err
}
func (c *testWebhookClient) Close() error { c.closed++; return nil }

const privateHookURL = "https://open.feishu.cn/open-apis/bot/v2/hook/FAKE_PRIVATE_ENDPOINT"
const privateHookSecret = "FAKE_PRIVATE_WEBHOOK_SIGNATURE"

func hookCredentialsBody(t *testing.T, endpoint string, secret *string) string {
	t.Helper()
	data, err := json.Marshal(api.NotifierCredentialsInput{URL: endpoint, SigningSecret: secret})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func hookChannel(t *testing.T, cfg *config.Config, id string) config.Notifier {
	t.Helper()
	for _, channel := range cfg.Notifiers {
		if channel.ID == id {
			return channel
		}
	}
	t.Fatalf("channel %s missing", id)
	return config.Notifier{}
}

func TestWebhookCredentialAPIIsAdminOnlyAndNeverReturnsValues(t *testing.T) {
	f := newAPIFixture(t)
	secret := privateHookSecret
	body := hookCredentialsBody(t, privateHookURL, &secret)
	path := "/api/v1/notifiers/hook/credentials"
	f.request(t, "POST", path, "read", body, 403)
	f.request(t, "POST", path, "config", body, 403)
	if f.vault.setCalls != 0 {
		t.Fatal("non-admin wrote Webhook credentials")
	}
	response := f.request(t, "POST", path, "admin", body, 200)
	channel := hookChannel(t, f.manager.Snapshot(), "hook")
	if channel.Type != "feishu_webhook" || string(f.vault.values[channel.URLRef]) != privateHookURL || string(f.vault.values[channel.SecretRef]) != secret || f.hookFactories != 0 || len(f.client.sent) != 0 {
		t.Fatal("credentials were not stored privately or saving sent a message")
	}
	visible := response.Body.String() + f.request(t, "GET", "/api/v1/config/notifiers", "read", "", 200).Body.String() + string(f.files.MapFS["notifiers.yaml"].Data) + f.logs.String()
	for _, value := range []string{privateHookURL, privateHookSecret} {
		if strings.Contains(visible, value) {
			t.Fatalf("Webhook credential leaked: %q", value)
		}
	}
	if !strings.Contains(string(f.files.MapFS["notifiers.yaml"].Data), "preserve this comment") {
		t.Fatal("saving Webhook credentials lost existing configuration comments")
	}
	changed := f.manager.Snapshot()
	for i := range changed.Notifiers {
		if changed.Notifiers[i].ID == "hook" {
			changed.Notifiers[i].Template = "compact"
		}
	}
	encode := func() string {
		data, err := json.Marshal(config.NotifiersFile{FeishuApps: changed.FeishuApps, Notifiers: changed.Notifiers})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	f.request(t, "PUT", "/api/v1/config/notifiers", "config", encode(), 200)
	for i := range changed.Notifiers {
		if changed.Notifiers[i].ID == "hook" {
			changed.Notifiers[i].SecretRef = ""
		}
	}
	f.request(t, "PUT", "/api/v1/config/notifiers", "config", encode(), 403)
	if got := hookChannel(t, f.manager.Snapshot(), "hook"); got.SecretRef != channel.SecretRef || got.Template != "compact" {
		t.Fatalf("config scope changed credential references: %+v", got)
	}
}

func TestWebhookCredentialRotationPreservesOmittedSignatureAndRollsBackFailure(t *testing.T) {
	f := newAPIFixture(t)
	path := "/api/v1/notifiers/hook/credentials"
	secret := privateHookSecret
	f.request(t, "POST", path, "admin", hookCredentialsBody(t, privateHookURL, &secret), 200)
	first := hookChannel(t, f.manager.Snapshot(), "hook")
	nextURL := strings.Replace(privateHookURL, "PRIVATE_ENDPOINT", "ROTATED_ENDPOINT", 1)
	f.request(t, "POST", path, "admin", hookCredentialsBody(t, nextURL, nil), 200)
	second := hookChannel(t, f.manager.Snapshot(), "hook")
	if second.URLRef == first.URLRef || second.SecretRef != first.SecretRef || string(f.vault.values[first.URLRef]) != privateHookURL || string(f.vault.values[second.URLRef]) != nextURL {
		t.Fatal("rotation lost previous queued credentials or omitted signing key")
	}
	f.vault.setError = errors.New(privateHookSecret)
	response := f.request(t, "POST", path, "admin", hookCredentialsBody(t, privateHookURL, nil), 500)
	if hookChannel(t, f.manager.Snapshot(), "hook") != second || strings.Contains(response.Body.String(), privateHookSecret) {
		t.Fatal("vault failure changed live configuration or leaked a secret")
	}
	f.vault.setError = nil
	f.files.failure = errors.New("fake disk failure")
	f.request(t, "POST", path, "admin", hookCredentialsBody(t, privateHookURL, nil), 500)
	if hookChannel(t, f.manager.Snapshot(), "hook") != second {
		t.Fatal("configuration write failure replaced live credential references")
	}
	f.files.failure = nil
	empty := ""
	f.request(t, "POST", path, "admin", hookCredentialsBody(t, nextURL, &empty), 200)
	if got := hookChannel(t, f.manager.Snapshot(), "hook"); got.SecretRef != "" || string(f.vault.values[first.SecretRef]) != secret {
		t.Fatal("clearing signing key did not preserve old queued credentials")
	}
	if strings.Contains(f.logs.String(), privateHookURL) || strings.Contains(f.logs.String(), privateHookSecret) {
		t.Fatal("failed credential save exposed values to logs")
	}
}

func TestWebhookSignatureSaveFailureDoesNotCreateChannel(t *testing.T) {
	f := newAPIFixture(t)
	f.vault.setError, f.vault.failSetAt = errors.New(privateHookSecret), 2
	secret := privateHookSecret
	response := f.request(t, "POST", "/api/v1/notifiers/hook/credentials", "admin", hookCredentialsBody(t, privateHookURL, &secret), 500)
	if f.vault.setCalls != 2 || len(f.manager.Snapshot().Notifiers) != 1 || f.hookFactories != 0 || strings.Contains(response.Body.String()+f.logs.String(), privateHookSecret) {
		t.Fatal("partial vault write published a channel, sent a message or exposed its secret")
	}
}

type appWebhookHTTP func(*http.Request) (*http.Response, error)

func (f appWebhookHTTP) Do(r *http.Request) (*http.Response, error) { return f(r) }

func hookServerFactory(t *testing.T, server *httptest.Server, now func() time.Time) WebhookFactory {
	t.Helper()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return func(endpoint, secret []byte) (feishu.WebhookClient, error) {
		return feishu.NewWebhookClient(endpoint, secret, feishu.WebhookOptions{Now: now, HTTPClient: appWebhookHTTP(func(r *http.Request) (*http.Response, error) {
			r = r.Clone(r.Context())
			r.URL.Host, r.URL.Scheme = base.Host, base.Scheme
			return client.Do(r)
		})})
	}
}

func TestWebhookHTTPTestSendsCardAndPersistsWithoutProviderMessageID(t *testing.T) {
	f := newAPIFixture(t)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || string(body["msg_type"]) != `"interactive"` || string(body["timestamp"]) != strconv.Quote(strconv.FormatInt(f.now.Unix(), 10)) || len(body["sign"]) == 0 || len(body["card"]) == 0 || r.Header.Get("Authorization") != "" {
			t.Errorf("bad custom-bot request: %s %v", body, err)
		}
		fmt.Fprint(w, `{"code":0}`)
	}))
	defer server.Close()
	f.hookFactory = hookServerFactory(t, server, func() time.Time { return f.now })
	secret := privateHookSecret
	f.request(t, "POST", "/api/v1/notifiers/hook/credentials", "admin", hookCredentialsBody(t, privateHookURL, &secret), 200)
	path := "/api/v1/notifiers/hook/test"
	f.request(t, "POST", path, "read", `{}`, 403)
	response := f.request(t, "POST", path, "config", `{"template":"compact"}`, 200)
	var result api.Confirmation
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || !result.OK || calls != 1 || len(f.client.sent) != 0 {
		t.Fatalf("test result=%+v,%v calls=%d", result, err, calls)
	}
	query := `{"sql":"SELECT ok,message_id,skipped FROM notifications WHERE notifier_id='hook'"}`
	audit := f.request(t, "POST", "/api/v1/query", "read", query, 200)
	if !strings.Contains(audit.Body.String(), `[[1,"",0]]`) {
		t.Fatalf("webhook submission was not recorded truthfully: %s", audit.Body.String())
	}
	for _, value := range f.vault.readBuffers {
		if !bytes.Equal(value, make([]byte, len(value))) {
			t.Fatal("Webhook credential read buffer not cleared")
		}
	}
	f.request(t, "POST", path, "config", `{}`, 429)
}

func TestWebhookCredentialValidationDoesNotStoreOrEchoInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		path, body string
	}{
		{"/api/v1/notifiers/hook/credentials", `{"url":"http://open.feishu.cn/open-apis/bot/v2/hook/FAKE_PRIVATE_ENDPOINT"}`},
		{"/api/v1/notifiers/hook/credentials", `{"url":"https://example.invalid/FAKE_PRIVATE_ENDPOINT"}`},
		{"/api/v1/notifiers/hook/credentials", `{"type":"unsupported","url":"` + privateHookURL + `"}`},
		{"/api/v1/notifiers/hook/credentials", `{"url":"` + privateHookURL + `","signingSecret":["FAKE_PRIVATE_WEBHOOK_SIGNATURE"]}`},
		{"/api/v1/notifiers/fake-channel/credentials", `{"url":"` + privateHookURL + `"}`},
	} {
		f := newAPIFixture(t)
		response := f.request(t, "POST", tc.path, "admin", tc.body, 400)
		visible := response.Body.String() + f.logs.String()
		if f.vault.setCalls != 0 || len(f.manager.Snapshot().Notifiers) != 1 || strings.Contains(visible, "FAKE_PRIVATE_ENDPOINT") || strings.Contains(visible, privateHookSecret) {
			t.Fatalf("invalid credentials were stored, echoed or changed config: %s", response.Body.String())
		}
	}
}

func TestWebhookPreflightRejectsMissingOrEmptyCredentialsWithoutNetwork(t *testing.T) {
	for _, values := range []map[string][]byte{
		{},
		{"hook-url": []byte("invalid " + privateHookURL)},
		{"hook-url": []byte(privateHookURL)},
		{"hook-url": []byte(privateHookURL), "hook-sign": {}},
	} {
		vault := &apiVault{values: values}
		calls := 0
		factory := func([]byte, []byte) (feishu.WebhookClient, error) { calls++; return &testWebhookClient{}, nil }
		_, err := openWebhook(context.Background(), vault, factory, config.Notifier{Type: "feishu_webhook", URLRef: "hook-url", SecretRef: "hook-sign"})
		if err == nil || calls != 0 || strings.Contains(err.Error(), privateHookURL) {
			t.Fatalf("invalid vault values reached network: calls=%d err=%v", calls, err)
		}
		for _, value := range vault.readBuffers {
			if !bytes.Equal(value, make([]byte, len(value))) {
				t.Fatal("failed preflight retained a plaintext credential buffer")
			}
		}
	}
}

func TestWebhookQueueUsesTemplateFallbackAndAuditsBothAttempts(t *testing.T) {
	f := newRuntimeFixture(t)
	f.files["notifiers.yaml"].Data = []byte("notifiers: [{id: test-channel, type: feishu_webhook, urlRef: hook-url, secretRef: hook-sign}]\n")
	if err := f.opts.Config.(*config.Manager).Reload(); err != nil {
		t.Fatal(err)
	}
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Card json.RawMessage `json:"card"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if calls == 1 {
			if !bytes.Contains(body.Card, []byte("fake-invalid-component")) {
				t.Error("rule template override not applied to webhook")
			}
			fmt.Fprint(w, `{"code":9499}`)
			return
		}
		if bytes.Contains(body.Card, []byte("fake-invalid-component")) {
			t.Error("rejected template was not replaced with standard fallback")
		}
		fmt.Fprint(w, `{"code":0}`)
	}))
	defer server.Close()
	q := policyQueue(t, f)
	q.secrets = &apiVault{values: map[string][]byte{"hook-url": []byte(privateHookURL), "hook-sign": []byte(privateHookSecret)}}
	q.webhookFactory = hookServerFactory(t, server, f.clock.Now)
	alert := seedUnsent(t, f, false)
	if err := q.Enqueue(context.Background(), alert); err != nil {
		t.Fatal(err)
	}
	close(q.jobs)
	if err := q.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, second := receiveRuntime(t, f.db.attempts), receiveRuntime(t, f.db.attempts)
	if first.OK || !second.OK || first.Template != "custom" || second.Template != "standard" || calls != 2 || f.factories.Load() != 0 || second.MessageID != "" {
		t.Fatalf("fallback audits: %+v %+v calls=%d", first, second, calls)
	}
	for _, attempt := range []notify.Attempt{first, second} {
		if strings.Contains(attempt.Error, privateHookURL) || strings.Contains(attempt.Error, privateHookSecret) {
			t.Fatal("webhook credentials leaked to persisted error")
		}
	}
}
