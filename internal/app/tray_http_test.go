package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/rules"
)

type trayHTTPRoundTrip func(*http.Request) (*http.Response, error)

func (f trayHTTPRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTrayHTTPReaderUsesExistingProtectedAPIAndTracksTriggerRecovery(t *testing.T) {
	f := newAPIFixture(t)
	server := httptest.NewServer(f.server)
	defer server.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	authenticate := func(scope string) http.RoundTripper {
		return trayHTTPRoundTrip(func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodGet || request.URL.Path != "/api/v1/alerts" || request.URL.Query().Get("state") != rules.StateFiring ||
				request.Header.Get("Authorization") != "" || request.URL.RawQuery != "state=firing" {
				t.Errorf("unexpected tray read: %s %s", request.Method, request.URL.Redacted())
			}
			authenticated := request.Clone(request.Context())
			authenticated.Header.Set("Authorization", "Bearer fake-"+scope)
			return transport.RoundTrip(authenticated)
		})
	}
	alert := rules.Alert{ID: "fake-tray-alert", RuleID: "down", CollectorID: "fake-host", TargetID: "web", State: rules.StateFiring, Severity: "critical", StartedAt: f.now.Add(-time.Minute).UnixMilli(), Summary: "虚构托盘故障"}
	if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, TS: alert.StartedAt, Kind: rules.StateFiring}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []api.Scope{api.ScopeRead, api.ScopeConfig, api.ScopeAdmin} {
		reader, err := NewTrayHTTPReader(server.URL, authenticate(string(scope)))
		if err != nil {
			t.Fatal(err)
		}
		status, err := reader.Read(context.Background())
		if err != nil || status.Firing != 1 || status.Scope != scope || status.PanelURL != server.URL {
			t.Fatalf("scope=%s status=%+v err=%v", scope, status, err)
		}
	}
	reader, err := NewTrayHTTPReader(server.URL, authenticate("read"))
	if err != nil {
		t.Fatal(err)
	}
	alert.State, alert.ResolvedAt = rules.StateResolved, f.now.UnixMilli()
	if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, TS: alert.ResolvedAt, Kind: rules.StateResolved}); err != nil {
		t.Fatal(err)
	}
	status, err := reader.Read(context.Background())
	if err != nil || status.Firing != 0 {
		t.Fatalf("resolved alert still appeared as firing: %+v %v", status, err)
	}
	unauthorized, err := NewTrayHTTPReader(server.URL, authenticate("missing"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unauthorized.Read(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("missing credentials were accepted: %v", err)
	}
	if f.files.writes != 0 || len(f.client.sent) != 0 || len(f.toast.values) != 0 || f.hookFactories != 0 || f.vault.setCalls != 0 {
		t.Fatal("status reading changed configuration, credentials or notification state")
	}
}

func TestTrayHTTPReaderDoesNotFollowRedirectOrExposeProviderBody(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationCalls.Add(1)
		w.Write([]byte("fake-private-token"))
	}))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
		w.Write([]byte("fake-private-token"))
	}))
	defer redirect.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	reader, err := NewTrayHTTPReader(redirect.URL, transport)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(context.Background()); err == nil || !strings.Contains(err.Error(), "307") || strings.Contains(err.Error(), "fake-private-token") {
		t.Fatalf("redirect response was not safely rejected: %v", err)
	}
	if destinationCalls.Load() != 0 {
		t.Fatal("reader followed a redirect to another destination")
	}
}

type trackedTrayHTTPBody struct {
	io.Reader
	closed bool
}

func (b *trackedTrayHTTPBody) Close() error { b.closed = true; return nil }

func TestTrayHTTPReaderRejectsInvalidSnapshotsAndClosesBodies(t *testing.T) {
	for _, test := range []struct {
		name, body, scope string
		status            int
	}{
		{"null", "null", "read", 200},
		{"object", "{}", "read", 200},
		{"broken", "[", "read", 200},
		{"resolved", `[{"state":"resolved"}]`, "read", 200},
		{"missing-state", "[{}]", "read", 200},
		{"unknown-scope", "[]", "owner", 200},
		{"missing-scope", "[]", "", 200},
		{"oversized", "[" + strings.Repeat(" ", 16*1024*1024), "read", 200},
		{"denied", "fake-private-token", "", 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &trackedTrayHTTPBody{Reader: strings.NewReader(test.body)}
			reader, err := NewTrayHTTPReader("http://127.0.0.1:7444", trayHTTPRoundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Header: http.Header{"X-Lgmon-Scope": []string{test.scope}}, Body: body}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Read(context.Background()); err == nil || strings.Contains(err.Error(), "fake-private-token") {
				t.Fatalf("invalid snapshot was accepted or exposed provider content: %v", err)
			}
			if !body.closed {
				t.Fatal("reader left response body open")
			}
		})
	}
}

func TestTrayHTTPReaderPropagatesCancellationAndRedactsNetworkErrors(t *testing.T) {
	started := make(chan struct{})
	reader, err := NewTrayHTTPReader("http://127.0.0.1:7444", trayHTTPRoundTrip(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := reader.Read(ctx); done <- err }()
	receiveRuntime(t, started)
	cancel()
	if err := receiveRuntime(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("HTTP request ignored cancellation: %v", err)
	}
	reader, err = NewTrayHTTPReader("http://127.0.0.1:7444", trayHTTPRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("fake-private-token")
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Read(context.Background()); err == nil || strings.Contains(err.Error(), "fake-private-token") {
		t.Fatalf("network error escaped redaction: %v", err)
	}
}
