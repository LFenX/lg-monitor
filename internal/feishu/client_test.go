package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

const fakeSecret = "fake-sdk-AppSecret-never-log-this"

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func tokenResponse(w http.ResponseWriter, token string, expire int) {
	writeJSON(w, map[string]any{"code": 0, "tenant_access_token": token, "expire": expire})
}
func newTestClient(t *testing.T, handler http.HandlerFunc, options Options) Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	options.BaseURL, options.HTTPClient = server.URL, server.Client()
	client, err := NewClient(config.FeishuApp{AppID: "cli_fake_not_real", Domain: "feishu", Proxy: "none"}, []byte(fakeSecret), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
func fixtureData(t *testing.T, name string) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name + ".cli.json")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &body); err != nil || !body.OK {
		t.Fatalf("invalid sanitized CLI fixture: %v", err)
	}
	return body.Data
}

func TestTokenCacheExpiryAndServerInvalidation(t *testing.T) {
	var now atomic.Int64
	now.Store(1000000)
	var auth, botCalls atomic.Int32
	var reject atomic.Bool
	bot := fixtureData(t, "bot")
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["app_id"] != "cli_fake_not_real" || body["app_secret"] != fakeSecret {
				t.Error("SDK did not send the configured fake credentials")
			}
			tokenResponse(w, "fake-token-"+string(rune('0'+auth.Add(1))), 10)
			return
		}
		botCalls.Add(1)
		if reject.Swap(false) {
			writeJSON(w, map[string]any{"code": 99991663, "msg": "Invalid access token"})
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer fake-token-") {
			t.Error("SDK did not attach tenant authorization")
		}
		writeJSON(w, map[string]any{"code": 0, "bot": bot})
	}, Options{Now: func() time.Time { return time.UnixMilli(now.Load()) }})
	first, err := client.TenantToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.TenantToken(context.Background())
	if err != nil || first != second || auth.Load() != 1 {
		t.Fatal("token was not cached")
	}
	now.Add(11000)
	third, err := client.TenantToken(context.Background())
	if err != nil || third == first || auth.Load() != 2 {
		t.Fatal("expired token was not refreshed")
	}
	reject.Store(true)
	info, err := client.BotInfo(context.Background())
	if err != nil || info.Name != "fixture-bot-not-real" || info.ActivateStatus != 2 || auth.Load() != 3 || botCalls.Load() != 2 {
		t.Fatalf("invalid token recovery failed: %+v, %v auth=%d bot=%d", info, err, auth.Load(), botCalls.Load())
	}
}

func TestClientsDoNotShareCredentialCaches(t *testing.T) {
	var callsA, callsB int
	makeClient := func(token string, calls *int) Client {
		return newTestClient(t, func(w http.ResponseWriter, r *http.Request) { *calls++; tokenResponse(w, token, 7200) }, Options{})
	}
	a, b := makeClient("fake-token-A", &callsA), makeClient("fake-token-B", &callsB)
	for _, test := range []struct {
		client Client
		want   string
	}{{a, "fake-token-A"}, {b, "fake-token-B"}, {a, "fake-token-A"}} {
		got, err := test.client.TenantToken(context.Background())
		if err != nil || got != test.want {
			t.Fatalf("credential cache crossed clients: %v", err)
		}
	}
	if callsA != 1 || callsB != 1 {
		t.Fatalf("cache misses A=%d B=%d", callsA, callsB)
	}
}

func TestScopeParsingAndApplyURL(t *testing.T) {
	data := fixtureData(t, "scopes")
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			tokenResponse(w, "fake-token-not-real", 7200)
			return
		}
		writeJSON(w, map[string]any{"code": 0, "data": data})
	}, Options{})
	scopes, err := client.AppScopes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Scopes []struct {
			Name  string `json:"scope_name"`
			Type  string `json:"scope_type"`
			Grant int    `json:"grant_status"`
		} `json:"scopes"`
	}
	_ = json.Unmarshal(data, &source)
	if len(scopes) != len(source.Scopes) {
		t.Fatal("scope response was truncated")
	}
	for i, scope := range scopes {
		if scope.Name != source.Scopes[i].Name || scope.Type != source.Scopes[i].Type || scope.Granted != (source.Scopes[i].Grant == 1) {
			t.Fatalf("scope mismatch: %+v", scope)
		}
	}
	want := "https://open.feishu.cn/page/scope-apply?clientID=cli_xxx&scopes=hire%3Ajob%3Areadonly%2Chire%3Apeople_cli"
	if got := ApplyURL("cli_xxx", []string{"hire:job:readonly", "hire:people_cli"}); got != want {
		t.Fatalf("ApplyURL() = %q", got)
	}
}

func TestDisabledBotStatusAndStructuredMissingScope(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "tenant_access_token") {
				tokenResponse(w, "fake-token-not-real", 7200)
				return
			}
			if disabled {
				writeJSON(w, map[string]any{"code": 0, "bot": map[string]any{"activate_status": 1, "app_name": "fake-bot"}})
				return
			}
			writeJSON(w, map[string]any{"code": 99991672, "msg": "Access denied", "error": map[string]any{"permission_violations": []any{map[string]string{"type": "action", "subject": "im:chat:read"}}}})
		}, Options{})
		_, err := client.BotInfo(context.Background())
		var failure *Error
		if !errors.As(err, &failure) {
			t.Fatalf("bot error=%v", err)
		}
		if disabled {
			if failure.Kind != BotDisabled {
				t.Fatalf("disabled bot=%v", err)
			}
		} else {
			if failure.Kind != MissingScopes || !reflect.DeepEqual(failure.Scopes, []string{"im:chat:read"}) {
				t.Fatalf("structured scope error=%+v", failure)
			}
		}
	}
}

func TestChatPaginationAndEmptyRealCLIData(t *testing.T) {
	for _, empty := range []bool{true, false} {
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "tenant_access_token") {
				tokenResponse(w, "fake-token-not-real", 7200)
				return
			}
			if empty {
				writeJSON(w, map[string]any{"code": 0, "data": fixtureData(t, "chats")})
				return
			}
			if r.URL.Query().Get("page_size") != "100" {
				t.Error("page size was not supplied")
			}
			if r.URL.Query().Get("page_token") == "" {
				writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]string{"chat_id": "oc_fake_1", "name": "first"}}, "has_more": true, "page_token": "fake-next"}})
			} else {
				writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]string{"chat_id": "oc_fake_1", "name": "first"}, map[string]string{"chat_id": "oc_fake_2", "name": "second"}}, "has_more": false}})
			}
		}, Options{})
		chats, err := client.ListChats(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if !empty {
			want = 2
		}
		if len(chats) != want {
			t.Fatalf("chat count=%d,want=%d", len(chats), want)
		}
	}
}

func TestTypedFailuresAndSecretRedaction(t *testing.T) {
	credentials, err := os.ReadFile("testdata/invalid_credentials.actual.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		status int
		body   any
		kind   ErrorKind
		auth   bool
	}{
		{"credentials", 200, json.RawMessage(credentials), Credentials, true},
		{"missing scope", 400, map[string]any{"code": 99991672, "msg": "Access denied. One of the following scopes is required: [im:chat:read, im:message:send_as_bot] " + fakeSecret}, MissingScopes, false},
		{"bot disabled", 400, map[string]any{"code": 230006, "msg": "Bot ability is not activated. " + fakeSecret}, BotDisabled, false},
		{"not in chat", 400, map[string]any{"code": 230002, "msg": "Bot/User can NOT be out of the chat. " + fakeSecret}, NotInChat, false},
		{"frequency limit", 400, map[string]any{"code": 99991400, "msg": "request trigger frequency limit " + fakeSecret}, RateLimited, false},
		{"HTTP limit", 429, map[string]any{"code": 0, "msg": fakeSecret}, RateLimited, false},
		{"server error", 503, map[string]any{"code": 0, "msg": fakeSecret}, Network, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "tenant_access_token") && !test.auth {
					tokenResponse(w, "fake-token-not-real", 7200)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_ = json.NewEncoder(w).Encode(test.body)
			}, Options{Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
			var failure error
			if test.auth {
				_, failure = client.TenantToken(context.Background())
			} else {
				_, failure = client.SendCard(context.Background(), CardMessage{AlertID: "fake-alert", State: "firing", ChatID: "oc_fake_not_real", Card: json.RawMessage(`{"schema":"2.0"}`)})
			}
			var typed *Error
			if !errors.As(failure, &typed) || typed.Kind != test.kind {
				t.Fatalf("failure=%v, want %s", failure, test.kind)
			}
			encoded, _ := json.Marshal(typed)
			if strings.Contains(failure.Error(), fakeSecret) || bytes.Contains(encoded, []byte(fakeSecret)) || strings.Contains(logs.String(), fakeSecret) {
				t.Fatal("SDK or typed error exposed the fake secret")
			}
			if test.kind == MissingScopes && !reflect.DeepEqual(typed.Scopes, []string{"im:chat:read", "im:message:send_as_bot"}) {
				t.Fatalf("missing scopes=%v", typed.Scopes)
			}
		})
	}
}

func TestSendAndCreateUseOfficialSDKAndStableUUID(t *testing.T) {
	var uuids []string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			tokenResponse(w, "fake-token-not-real", 7200)
			return
		}
		if r.URL.Path == "/open-apis/im/v1/chats" {
			var body struct {
				Name  string   `json:"name"`
				Users []string `json:"user_id_list"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Name != "fake test group" || !reflect.DeepEqual(body.Users, []string{"ou_fake_not_real"}) {
				t.Error("create chat body is incorrect")
			}
			writeJSON(w, map[string]any{"code": 0, "data": map[string]string{"chat_id": "oc_fake_new", "name": body.Name}})
			return
		}
		var body struct {
			Type      string `json:"msg_type"`
			Content   string `json:"content"`
			UUID      string `json:"uuid"`
			ReceiveID string `json:"receive_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Type != "interactive" || !json.Valid([]byte(body.Content)) || body.ReceiveID != "oc_fake_not_real" || r.URL.Query().Get("receive_id_type") != "chat_id" {
			t.Error("SDK message payload is incorrect")
		}
		uuids = append(uuids, body.UUID)
		writeJSON(w, map[string]any{"code": 0, "data": map[string]string{"message_id": "om_fake_not_real"}})
	}, Options{})
	request := CardMessage{AlertID: "fake-alert", State: "firing", ChatID: "oc_fake_not_real", Card: json.RawMessage(`{"schema":"2.0","body":{"elements":[]}}`)}
	for i := 0; i < 2; i++ {
		result, err := client.SendCard(context.Background(), request)
		if err != nil || result.MessageID != "om_fake_not_real" {
			t.Fatalf("send=%+v,%v", result, err)
		}
	}
	request.State = "resolved"
	if _, err := client.SendCard(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(uuids) != 3 || uuids[0] != uuids[1] || uuids[0] == uuids[2] || len(uuids[0]) > 50 {
		t.Fatalf("idempotency keys=%v", uuids)
	}
	if chat, err := client.CreateChat(context.Background(), NewChat{Name: "fake test group", UserIDs: []string{"ou_fake_not_real"}}); err != nil || chat.ID != "oc_fake_new" {
		t.Fatalf("create=%+v,%v", chat, err)
	}
}

func TestTimeoutAndConnectionFailureAreSafe(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := client.TenantToken(ctx)
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind != Network || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout=%v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()
	refused, err := NewClient(config.FeishuApp{AppID: "cli_fake_not_real", Domain: "feishu", Proxy: "none"}, []byte(fakeSecret), Options{BaseURL: address})
	if err != nil {
		t.Fatal(err)
	}
	defer refused.Close()
	_, err = refused.TenantToken(context.Background())
	if !errors.As(err, &typed) || typed.Kind != Network {
		t.Fatalf("connection failure=%v", err)
	}
}

func TestNoneProxyBypassesEnvironmentForNonLoopbackHost(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://fake-proxy.invalid:9999")
	t.Setenv("HTTPS_PROXY", "http://fake-proxy.invalid:9999")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { tokenResponse(w, "fake-token-not-real", 7200) }))
	defer server.Close()
	client, err := NewClient(config.FeishuApp{AppID: "cli_fake_not_real", Domain: "feishu", Proxy: "none"}, []byte(fakeSecret), Options{BaseURL: "http://fake-feishu.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	transport := client.(*apiClient).config.HttpClient.(safeHTTP).client.(*http.Client).Transport.(*http.Transport)
	var address string
	transport.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
		address = target
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "http://"))
	}
	if _, err := client.TenantToken(context.Background()); err != nil || address != "fake-feishu.invalid:80" {
		t.Fatalf("none proxy dialed %q: %v", address, err)
	}
}

var _ io.Closer = Client(nil)
