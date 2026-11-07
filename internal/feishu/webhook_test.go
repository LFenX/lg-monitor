package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type webhookHTTPFunc func(*http.Request) (*http.Response, error)

func (f webhookHTTPFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

type webhookRoundTrip func(*http.Request) (*http.Response, error)

func (f webhookRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const fakeHookURL = "https://open.feishu.cn/open-apis/bot/v2/hook/fake-not-real"

func webhookRedirectTo(t *testing.T, server *httptest.Server) webhookHTTPFunc {
	t.Helper()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = base.Scheme, base.Host
		return client.Do(r)
	}
}

func TestWebhookCardAndIndependentSignatureVector(t *testing.T) {
	for _, signed := range []bool{false, true} {
		t.Run(fmt.Sprint(signed), func(t *testing.T) {
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != "/open-apis/bot/v2/hook/fake-not-real" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "" {
					t.Errorf("incorrect webhook request: %s %s", r.Method, r.URL.Path)
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if string(body["msg_type"]) != `"interactive"` || string(body["card"]) != `{"schema":"2.0","body":{"elements":[]}}` {
					t.Errorf("wrong card envelope: %s", body)
				}
				if signed {
					// The vector was calculated independently with .NET HMACSHA256 over an empty message.
					if string(body["timestamp"]) != `"1599360473"` || string(body["sign"]) != `"tyB3tSckXO1oWlRLKIE8ADjo+c1Aouqc/wjS3g2+PNo="` || len(body) != 4 {
						t.Errorf("wrong signed envelope: %s", body)
					}
				} else if len(body) != 2 {
					t.Errorf("unsigned body contains extra fields: %s", body)
				}
				fmt.Fprint(w, `{"code":0,"data":{},"msg":"success"}`)
			}))
			defer server.Close()
			var secret []byte
			if signed {
				secret = []byte("fake-signing-secret")
			}
			endpoint := []byte(fakeHookURL)
			client, err := NewWebhookClient(endpoint, secret, WebhookOptions{HTTPClient: webhookRedirectTo(t, server), Now: func() time.Time { return time.Unix(1599360473, 999) }})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			clear(secret)
			clear(endpoint)
			result, err := client.SendCard(context.Background(), CardMessage{AlertID: "fake-alert", ChatID: "unused-fake-chat", Card: json.RawMessage(`{"schema":"2.0","body":{"elements":[]}}`)})
			if err != nil || result.MessageID != "" || calls != 1 {
				t.Fatalf("send=%+v,%v calls=%d", result, err, calls)
			}
		})
	}
}

func TestWebhookResponseClassificationAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		kind       ErrorKind
		retry      bool
	}{
		{"signature", `{"code":19021,"msg":"` + fakeHookURL + ` fake-signing-secret"}`, 200, Credentials, false},
		{"ip", `{"code":19022}`, 200, API, false},
		{"keyword", `{"code":19024}`, 200, API, false},
		{"card", `{"code":9499}`, 200, CardRejected, false},
		{"rate code", `{"code":11232}`, 200, RateLimited, true},
		{"rate status", "", 429, RateLimited, true},
		{"server", fakeHookURL, 503, Network, true},
		{"redirect", `{"code":0}`, 302, API, false},
		{"client status", `{"code":0}`, 403, API, false},
		{"missing code", `{"msg":"success"}`, 200, API, false},
		{"null code", `{"code":null}`, 200, API, false},
		{"bad json", "invalid " + fakeHookURL, 200, API, false},
		{"large response", strings.Repeat("x", 64*1024+1), 200, API, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client, err := NewWebhookClient([]byte(fakeHookURL), []byte("fake-signing-secret"), WebhookOptions{HTTPClient: webhookRedirectTo(t, server)})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			_, err = client.SendCard(context.Background(), CardMessage{Card: json.RawMessage(`{}`)})
			var failure *Error
			if !errors.As(err, &failure) || failure.Kind != tc.kind || Retryable(err) != tc.retry {
				t.Fatalf("failure=%+v retry=%v err=%v", failure, Retryable(err), err)
			}
			if strings.Contains(err.Error(), "fake-not-real") || strings.Contains(err.Error(), "fake-signing-secret") {
				t.Fatal("webhook response exposed credentials")
			}
		})
	}
}

func TestWebhookRejectsInvalidAddressesCardsAndClosedClient(t *testing.T) {
	for _, value := range []string{"", "http://open.feishu.cn/open-apis/bot/v2/hook/fake", "https://example.invalid/open-apis/bot/v2/hook/fake", fakeHookURL + "?secret=fake", fakeHookURL + "#fragment", "https://fake@open.feishu.cn/open-apis/bot/v2/hook/fake", fakeHookURL + "/extra", "https://open.feishu.cn/open-apis/bot/v2/hook/%66ake"} {
		if _, err := NewWebhookClient([]byte(value), nil, WebhookOptions{}); err == nil || strings.Contains(err.Error(), value) && value != "" {
			t.Fatalf("invalid URL accepted or echoed: %q, %v", value, err)
		}
	}
	var calls int
	client, err := NewWebhookClient([]byte(fakeHookURL), nil, WebhookOptions{HTTPClient: webhookHTTPFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("must not send")
	})})
	if err != nil {
		t.Fatal(err)
	}
	for _, card := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`{"x":"` + strings.Repeat("x", WebhookBodyLimit) + `"}`)} {
		if _, err := client.SendCard(context.Background(), CardMessage{Card: card}); err == nil {
			t.Fatalf("invalid card accepted: %d bytes", len(card))
		}
	}
	client.Close()
	client.Close()
	if _, err := client.SendCard(context.Background(), CardMessage{Card: json.RawMessage(`{}`)}); err == nil || calls != 0 {
		t.Fatalf("closed client or invalid card sent a request: %d, %v", calls, err)
	}
}

func TestWebhookDefaultClientDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client, err := NewWebhookClient([]byte(fakeHookURL), []byte("fake-signing-secret"), WebhookOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client.(*webhookClient).client.(*http.Client).Transport = webhookRoundTrip(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Host, r.URL.Scheme = base.Host, base.Scheme
		return transport.RoundTrip(r)
	})
	_, err = client.SendCard(context.Background(), CardMessage{Card: json.RawMessage(`{}`)})
	if err == nil || redirected.Load() != 0 {
		t.Fatalf("redirect sent credentials elsewhere: count=%d err=%v", redirected.Load(), err)
	}
}

func TestWebhookCancellationRetainsCauseWithoutURL(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := NewWebhookClient([]byte(fakeHookURL), nil, WebhookOptions{HTTPClient: webhookRedirectTo(t, server)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.SendCard(ctx, CardMessage{Card: json.RawMessage(`{}`)})
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "fake-not-real") {
			t.Fatalf("cancellation=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request did not stop")
	}
}
