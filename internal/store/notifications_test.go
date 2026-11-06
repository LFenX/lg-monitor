package store

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify"
)

type instantRetryWaiter struct{ delays []time.Duration }

func (w *instantRetryWaiter) Wait(_ context.Context, delay time.Duration) error {
	w.delays = append(w.delays, delay)
	return nil
}

func TestSDKRetriesPersistEveryAttempt(t *testing.T) {
	var mu sync.Mutex
	var uuids []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "expire": 7200, "tenant_access_token": "fake-token-not-real"})
			return
		}
		var body struct {
			UUID string `json:"uuid"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		uuids = append(uuids, body.UUID)
		attempt := len(uuids)
		mu.Unlock()
		if attempt <= 3 {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 99991400, "msg": "request trigger frequency limit"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"message_id": "om_fake_not_real"}})
	}))
	defer server.Close()
	client, err := feishu.NewClient(config.FeishuApp{AppID: "cli_fake_not_real", Domain: "feishu", Proxy: "none"}, []byte("fake-sdk-secret-not-real"), feishu.Options{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	s := openStore(t, Options{})
	alert := testAlert()
	if err := s.SaveTransition(context.Background(), alert, testEvent(alert)); err != nil {
		t.Fatal(err)
	}
	waiter := &instantRetryWaiter{}
	sender, err := notify.NewSender(notify.Options{Client: client, Recorder: s, Waiter: waiter, Now: func() time.Time { return time.UnixMilli(5000) }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := sender.Send(context.Background(), notify.Request{NotifierID: "fake-notifier", Template: "standard", Message: feishu.CardMessage{AlertID: alert.ID, State: alert.State, ChatID: "oc_fake_not_real", Card: json.RawMessage(`{"schema":"2.0"}`)}})
	if err != nil || result.MessageID != "om_fake_not_real" || count(t, s, "notifications") != 4 {
		t.Fatalf("send audit=%+v,%v", result, err)
	}
	if !reflect.DeepEqual(waiter.delays, []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second}) {
		t.Fatalf("retry delays=%v", waiter.delays)
	}
	var success int
	var messageID string
	if err := s.db.QueryRow(`SELECT sum(ok),max(message_id) FROM notifications`).Scan(&success, &messageID); err != nil || success != 1 || messageID != "om_fake_not_real" {
		t.Fatalf("audit result: success=%d id=%q err=%v", success, messageID, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(uuids) != 4 {
		t.Fatalf("wire attempts=%d", len(uuids))
	}
	for _, uuid := range uuids {
		if uuid != uuids[0] {
			t.Fatal("wire retry changed idempotency key")
		}
	}
}

func TestNotificationAuditUpdatesOnlySuccessfulDeliveryTime(t *testing.T) {
	s := openStore(t, Options{})
	alert := testAlert()
	if err := s.SaveTransition(context.Background(), alert, testEvent(alert)); err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []notify.Attempt{
		{ID: "attempt-1", AlertID: alert.ID, NotifierID: "fake-notifier", Template: "standard", TS: 2000, Error: "网络失败"},
		{ID: "attempt-2", AlertID: alert.ID, NotifierID: "fake-notifier", Template: "standard", TS: 3000, OK: true, MessageID: "om_fake_not_real"},
		{ID: "attempt-3", AlertID: alert.ID, NotifierID: "fake-notifier", Template: "standard", TS: 4000, Error: "网络失败"},
	} {
		if err := s.RecordNotification(context.Background(), attempt); err != nil {
			t.Fatal(err)
		}
	}
	var notified int64
	if err := s.db.QueryRow(`SELECT last_notified_at FROM alerts WHERE id=?`, alert.ID).Scan(&notified); err != nil || notified != 3000 || count(t, s, "notifications") != 3 {
		t.Fatalf("notification audit time=%d, err=%v", notified, err)
	}
}
