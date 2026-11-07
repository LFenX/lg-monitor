package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	validator "github.com/santhosh-tekuri/jsonschema/v6"
)

// These are public reference values published by Standard Webhooks, not application credentials.
const referenceWebhookKey = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
const referenceWebhookID = "msg_p5jXN8AQM9LWM0D4loKWxJek"
const referenceWebhookTimestamp = "1614265330"
const referenceWebhookBody = `{"test": 2432232314}`
const referenceWebhookSignature = "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="

func TestStandardWebhooksPublicReferenceVectorAndReceiver(t *testing.T) {
	key, err := DecodeWebhookSecret([]byte(referenceWebhookKey))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	if got := WebhookSignature(key, referenceWebhookID, referenceWebhookTimestamp, []byte(referenceWebhookBody)); got != referenceWebhookSignature {
		t.Fatalf("signature=%s", got)
	}
	verify := func(id, timestamp string, body []byte, signature string, now time.Time) bool {
		at, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil || now.Sub(time.Unix(at, 0)).Abs() > 5*time.Minute {
			return false
		}
		mac := hmac.New(sha256.New, key)
		fmt.Fprintf(mac, "%s.%s.", id, timestamp)
		mac.Write(body)
		given, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(signature, "v1,"))
		return strings.HasPrefix(signature, "v1,") && err == nil && hmac.Equal(mac.Sum(nil), given)
	}
	now := time.Unix(1614265330, 0)
	if !verify(referenceWebhookID, referenceWebhookTimestamp, []byte(referenceWebhookBody), referenceWebhookSignature, now) {
		t.Fatal("receiver rejected reference")
	}
	for _, tc := range []struct {
		id, ts, body string
		now          time.Time
	}{
		{referenceWebhookID + "x", referenceWebhookTimestamp, referenceWebhookBody, now},
		{referenceWebhookID, "1614265331", referenceWebhookBody, now},
		{referenceWebhookID, referenceWebhookTimestamp, `{"test":2432232314}`, now},
		{referenceWebhookID, referenceWebhookTimestamp, referenceWebhookBody, now.Add(301 * time.Second)},
		{referenceWebhookID, referenceWebhookTimestamp, referenceWebhookBody, now.Add(-301 * time.Second)},
	} {
		if verify(tc.id, tc.ts, []byte(tc.body), referenceWebhookSignature, tc.now) {
			t.Fatal("receiver accepted altered or stale request")
		}
	}
}

func TestJSONWebhookRetriesRefreshSignatureWithStableIDAndDiscardResponse(t *testing.T) {
	now := time.Unix(1800000000, 0)
	key, _ := DecodeWebhookSecret([]byte(referenceWebhookKey))
	defer clear(key)
	var ids, timestamps, signatures []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		id, at := r.Header.Get("webhook-id"), r.Header.Get("webhook-timestamp")
		mac := hmac.New(sha256.New, key)
		fmt.Fprintf(mac, "%s.%s.", id, at)
		mac.Write(body)
		given, signatureErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(r.Header.Get("webhook-signature"), "v1,"))
		seconds, timestampErr := strconv.ParseInt(at, 10, 64)
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("User-Agent") != "lg-monitor/fake-version" || string(body) != referenceWebhookBody || signatureErr != nil || !hmac.Equal(mac.Sum(nil), given) || timestampErr != nil || now.Sub(time.Unix(seconds, 0)).Abs() > 5*time.Minute {
			t.Error("unexpected signed request")
		}
		ids = append(ids, id)
		timestamps = append(timestamps, at)
		signatures = append(signatures, r.Header.Get("webhook-signature"))
		if len(ids) == 1 {
			w.WriteHeader(500)
		} else {
			w.WriteHeader(204)
		}
		fmt.Fprint(w, "FAKE_PRIVATE_RESPONSE")
	}))
	defer server.Close()
	client, err := NewJSONWebhookClient([]byte(server.URL), []byte(referenceWebhookKey), JSONWebhookOptions{Now: func() time.Time { return now }, Version: "fake-version"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	recorder := &memoryRecorder{}
	var logs bytes.Buffer
	sender, _ := NewWebhookSender(WebhookSenderOptions{Client: client, Recorder: recorder, Now: func() time.Time { return now }, Waiter: webhookWaitFunc(func(_ context.Context, delay time.Duration) error {
		if delay != 2*time.Second {
			t.Fatal(delay)
		}
		now = now.Add(delay)
		return nil
	}), Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	request := WebhookRequest{AlertID: "fake-alert", State: "firing", EventKey: "firing:1800000000000", NotifierID: "fake-hook", Body: json.RawMessage(referenceWebhookBody)}
	if err := sender.Send(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256([]byte("fake-alert\x00firing\x00firing:1800000000000\x00fake-hook"))
	if len(ids) != 2 || ids[0] != hex.EncodeToString(expected[:20]) || ids[1] != ids[0] || timestamps[0] == timestamps[1] || signatures[0] == signatures[1] {
		t.Fatalf("IDs=%v timestamps=%v", ids, timestamps)
	}
	if len(recorder.attempts) != 2 || recorder.attempts[0].StatusCode != 500 || recorder.attempts[0].OK || !recorder.attempts[1].OK || recorder.attempts[1].StatusCode != 204 {
		t.Fatalf("audit=%+v", recorder.attempts)
	}
	if strings.Contains(fmt.Sprint(recorder.attempts)+logs.String(), "FAKE_PRIVATE_RESPONSE") {
		t.Fatal("response leaked")
	}
	// Reconstructing the sender after a restart keeps the same event identity.
	other, _ := NewWebhookSender(WebhookSenderOptions{Client: client, Recorder: recorder})
	if err := other.Send(context.Background(), request); err != nil || ids[2] != ids[0] {
		t.Fatalf("replay ID changed: %v", err)
	}
	if WebhookID(request.AlertID, request.State, "renotify:next", request.NotifierID) == ids[0] || WebhookID(request.AlertID, request.State, request.EventKey, "other") == ids[0] {
		t.Fatal("distinct notification identities collided")
	}
}

type webhookWaitFunc func(context.Context, time.Duration) error

func (f webhookWaitFunc) Wait(ctx context.Context, d time.Duration) error { return f(ctx, d) }

func TestJSONWebhookStatusRetryMatrixAndNoRedirect(t *testing.T) {
	for _, status := range []int{200, 299, 301, 307, 400, 401, 404, 408, 429, 500, 503, 599} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(status)
				fmt.Fprint(w, "FAKE_RESPONSE_BODY")
			}))
			defer server.Close()
			client, err := NewJSONWebhookClient([]byte(server.URL), nil, JSONWebhookOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			recorder, waiter := &memoryRecorder{}, &fakeWaiter{}
			sender, _ := NewWebhookSender(WebhookSenderOptions{Client: client, Recorder: recorder, Waiter: waiter})
			err = sender.Send(context.Background(), WebhookRequest{AlertID: "fake", State: "firing", NotifierID: "fake-hook", Body: json.RawMessage(`{}`)})
			want := 1
			if status == 408 || status == 429 || status >= 500 {
				want = 4
			}
			if calls != want || len(recorder.attempts) != want || (err == nil) != (status >= 200 && status < 300) {
				t.Fatalf("calls=%d audit=%d err=%v", calls, len(recorder.attempts), err)
			}
			if want == 4 && !reflect.DeepEqual(waiter.waits, []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second}) {
				t.Fatal(waiter.waits)
			}
			if status >= 300 && status < 400 && !strings.Contains(err.Error(), "对方要求跳转") {
				t.Fatal(err)
			}
			if err != nil && strings.Contains(err.Error(), "FAKE_RESPONSE_BODY") {
				t.Fatal("response echoed")
			}
		})
	}
}

type countedWebhookBody struct {
	remaining, read int
	closed          bool
}

func (b *countedWebhookBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), b.remaining)
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	b.remaining -= n
	b.read += n
	return n, nil
}
func (b *countedWebhookBody) Close() error { b.closed = true; return nil }

func TestJSONWebhookReadLimitTimeoutCancellationAndFilteredRetry(t *testing.T) {
	body := &countedWebhookBody{remaining: 9000}
	client, _ := NewJSONWebhookClient([]byte("http://fake.example.test"), nil, JSONWebhookOptions{HTTPClient: webhookDoer(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 15*time.Second {
			t.Error("missing 15s budget")
		}
		if r.Header.Get("webhook-signature") != "" {
			t.Error("unsigned request signed")
		}
		return &http.Response{StatusCode: 200, Body: body}, nil
	})})
	defer client.Close()
	if _, err := client.Send(context.Background(), "fake", json.RawMessage(`{}`)); err != nil || body.read != 4096 || !body.closed {
		t.Fatalf("body=%+v err=%v", body, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled, _ := NewJSONWebhookClient([]byte("http://fake.example.test"), nil, JSONWebhookOptions{HTTPClient: webhookDoer(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })})
	defer cancelled.Close()
	if _, err := cancelled.Send(ctx, "fake", json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	calls, allowed := 0, true
	failing, _ := NewJSONWebhookClient([]byte("http://fake.example.test"), nil, JSONWebhookOptions{HTTPClient: webhookDoer(func(r *http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("FAKE_PRIVATE_NETWORK") })})
	defer failing.Close()
	recorder := &memoryRecorder{}
	sender, _ := NewWebhookSender(WebhookSenderOptions{Client: failing, Recorder: recorder, Allow: func(context.Context) (bool, error) { return allowed, nil }, Waiter: webhookWaitFunc(func(context.Context, time.Duration) error { allowed = false; return nil })})
	if err := sender.Send(context.Background(), WebhookRequest{AlertID: "fake", State: "firing", NotifierID: "fake", Body: json.RawMessage(`{}`)}); err != nil || calls != 1 || len(recorder.attempts) != 1 || strings.Contains(recorder.attempts[0].Error, "FAKE_PRIVATE_NETWORK") {
		t.Fatalf("filter retry: calls=%d audit=%+v err=%v", calls, recorder.attempts, err)
	}
}

func TestJSONWebhookSecretAndURLValidation(t *testing.T) {
	for _, value := range []string{"", "fake", "whsec_ZmFrZQ==", "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 65)), referenceWebhookKey + "\n"} {
		if key, err := DecodeWebhookSecret([]byte(value)); err == nil {
			clear(key)
			t.Fatalf("accepted invalid key")
		}
	}
	for _, value := range []string{"file:///fake", "https://user:password@example.test", "http:///missing", "https://example.test/#fragment"} {
		if err := ValidateWebhookURL([]byte(value)); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
	for _, value := range []string{"http://127.0.0.1:8080/hook", "https://example.test/private?token=fake"} {
		if err := ValidateWebhookURL([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	secret, err := GenerateWebhookSecret(bytes.NewReader(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	key, err := DecodeWebhookSecret([]byte(secret))
	defer clear(key)
	if err != nil || len(key) != 32 {
		t.Fatal("generated key is invalid")
	}
	if _, err := GenerateWebhookSecret(strings.NewReader("short")); err == nil {
		t.Fatal("entropy failure hidden")
	}
}

func TestWebhookPayloadVariantsMatchSchema(t *testing.T) {
	data, err := os.ReadFile("../../schemas/webhook-payload.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema any
	if err = json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	compiler := validator.NewCompiler()
	if err = compiler.AddResource("https://example.test/payload", schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("https://example.test/payload")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"alert.firing", "alert.resolved", "alert.renotify", "alert.test"} {
		t.Run(kind, func(t *testing.T) {
			input := cards.Sample("firing")
			input.Alert.NotificationAt = 1800000000123
			input.Alert.Summary = strings.Repeat("中", 2001)
			input.Detail = strings.Repeat("文", 2001)
			input.Value = strings.Repeat("🙂", 2001)
			input.Alert.Flapping = true
			input.Target = config.Target{ID: "@collector"}
			input.Alert.TargetID = "@collector"
			input.Alert.StartedAt = 0
			input.Alert.ResolvedAt = 0
			input.DashboardURL = ""
			input.Host.Name = ""
			if kind == "alert.resolved" {
				input.Alert.State = "resolved"
				input.Alert.ResolvedAt = 1800000010123
			}
			if kind == "alert.renotify" {
				input.Alert.NotificationKey = "renotify:1800000000123"
			}
			payload := WebhookPayload(input, time.Unix(0, 0), kind == "alert.test")
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			var value any
			json.Unmarshal(raw, &value)
			if err = compiled.Validate(value); err != nil {
				t.Fatalf("%s\n%v", raw, err)
			}
			if payload.Type != kind || payload.Timestamp != "2027-01-15T08:00:00.123Z" || payload.Data.Alert.StartedAt != nil || payload.Data.DashboardURL != nil || payload.Data.Target.Tags == nil || len(payload.Data.Target.Tags) != 0 || payload.Data.Collector.Hostname != input.Alert.CollectorID || !payload.Data.Alert.Flapping {
				t.Fatalf("payload=%+v", payload)
			}
			for _, text := range []string{payload.Data.Alert.Summary, payload.Data.Alert.Detail, payload.Data.Alert.Value} {
				if utf8.RuneCountInString(text) != 2000 || !strings.HasSuffix(text, "…") {
					t.Fatal("Unicode truncation invalid")
				}
			}
			if kind == "alert.test" && !strings.Contains(payload.Data.Alert.Summary, "这是测试消息") {
				t.Fatal("test summary missing")
			}
			if strings.Contains(string(raw), "processes") || strings.Contains(string(raw), "logTail") {
				t.Fatal("long content included")
			}
		})
	}
}
