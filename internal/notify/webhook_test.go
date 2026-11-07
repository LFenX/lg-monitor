package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/feishu"
)

type webhookDoer func(*http.Request) (*http.Response, error)

func (f webhookDoer) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestWebhookDeliveryUsesExistingRetryAndAudit(t *testing.T) {
	const fakeURL = "https://open.feishu.cn/open-apis/bot/v2/hook/fake-private-webhook"
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Card json.RawMessage `json:"card"`
			Sign string          `json:"sign"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Card) == 0 || body.Sign == "" {
			t.Errorf("webhook payload invalid: %+v %v", body, err)
		}
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, fakeURL)
			return
		}
		fmt.Fprint(w, `{"code":0}`)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client, err := feishu.NewWebhookClient([]byte(fakeURL), []byte("fake-signing-secret"), feishu.WebhookOptions{HTTPClient: webhookDoer(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = base.Scheme, base.Host
		return server.Client().Do(r)
	})})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var logs bytes.Buffer
	recorder, waiter := &memoryRecorder{}, &fakeWaiter{}
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	sender, err := NewSender(Options{Client: client, Recorder: recorder, Waiter: waiter, Logger: logger, Now: func() time.Time { return time.UnixMilli(123456) }})
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest()
	request.Message.EventKey = "fake-event"
	result, err := sender.Send(context.Background(), request)
	if err != nil || result.MessageID != "" || calls != 2 || !reflect.DeepEqual(waiter.waits, []time.Duration{2 * time.Second}) || len(recorder.attempts) != 2 {
		t.Fatalf("send=%+v,%v calls=%d attempts=%d waits=%v", result, err, calls, len(recorder.attempts), waiter.waits)
	}
	for i, attempt := range recorder.attempts {
		if attempt.OK != (i == 1) || attempt.EventKey != "fake-event" || attempt.NotifierID != request.NotifierID || attempt.TS != 123456 || strings.Contains(attempt.Error, "fake-private-webhook") {
			t.Fatalf("bad audit=%+v", attempt)
		}
	}
	if strings.Contains(logs.String(), "fake-private-webhook") || strings.Contains(logs.String(), "fake-signing-secret") {
		t.Fatal("webhook credentials leaked to logs")
	}
}
