// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeStreamTicker struct {
	ticks   chan time.Time
	stopped bool
}

func (t *fakeStreamTicker) C() <-chan time.Time { return t.ticks }
func (t *fakeStreamTicker) Stop()               { t.stopped = true }

type fakeStreamClock struct{ ticker *fakeStreamTicker }

func (c fakeStreamClock) NewTicker(time.Duration) StreamTicker { return c.ticker }

type streamWriter struct {
	headers http.Header
	buffer  bytes.Buffer
	frames  chan string
}

func (w *streamWriter) Header() http.Header         { return w.headers }
func (w *streamWriter) WriteHeader(int)             {}
func (w *streamWriter) Write(p []byte) (int, error) { return w.buffer.Write(p) }
func (w *streamWriter) Flush()                      { w.frames <- w.buffer.String(); w.buffer.Reset() }

func receiveFrame(t *testing.T, frames <-chan string) string {
	t.Helper()
	select {
	case frame := <-frames:
		return frame
	case <-time.After(time.Second):
		t.Fatal("SSE did not flush")
		return ""
	}
}

func TestSSEPublishesAllEventKindsAndReleasesCancelledSubscriber(t *testing.T) {
	ticker := &fakeStreamTicker{ticks: make(chan time.Time, 1)}
	s := serverFixture(t, func(opts *ServerOptions) { opts.StreamClock = fakeStreamClock{ticker} })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("GET", "http://127.0.0.1:7444/api/v1/stream", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer fake-read")
	w := &streamWriter{headers: http.Header{}, frames: make(chan string, 10)}
	done := make(chan struct{})
	go func() { defer close(done); s.ServeHTTP(w, r) }()
	if frame := receiveFrame(t, w.frames); frame != ": connected\n\n" || !strings.HasPrefix(w.headers.Get("Content-Type"), "text/event-stream") {
		t.Fatal("SSE handshake missing")
	}
	for _, kind := range []string{"target", "alert", "sample", "ruleErrors", "cardAction"} {
		if err := s.options.Broker.Publish(kind, map[string]string{"value": "line\nvalue"}); err != nil {
			t.Fatal(err)
		}
		frame := receiveFrame(t, w.frames)
		if !strings.Contains(frame, "event: "+kind+"\n") || !strings.Contains(frame, `{"value":"line\nvalue"}`) {
			t.Fatalf("event framing: %q", frame)
		}
	}
	ticker.ticks <- time.Time{}
	if frame := receiveFrame(t, w.frames); frame != ": heartbeat\n\n" {
		t.Fatal("missing heartbeat")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE did not stop on cancellation")
	}
	if !ticker.stopped || len(s.options.Broker.clients) != 0 {
		t.Fatal("SSE subscriber or ticker leaked")
	}
}

func TestBrokerDropsSlowReadersWithoutBlockingIngestion(t *testing.T) {
	b := NewBroker()
	events, unsubscribe := b.subscribe(false, time.Time{})
	defer unsubscribe()
	for i := 0; i < 65; i++ {
		if err := b.Publish("sample", i); err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	for range events {
		count++
	}
	if count != 64 || len(b.clients) != 0 {
		t.Fatal("overflowing subscriber stayed attached")
	}
	b.Close()
	if err := b.Publish("sample", 1); err == nil {
		t.Fatal("closed broker accepted an event")
	}
}

func TestSSEStopsWhenPersistentTokenIsRevoked(t *testing.T) {
	active := true
	ticker := &fakeStreamTicker{ticks: make(chan time.Time, 1)}
	s := serverFixture(t, func(opts *ServerOptions) {
		opts.StreamClock = fakeStreamClock{ticker}
		opts.Auth.tokens = TokenLookupFunc(func(context.Context, [32]byte) (Principal, bool, error) {
			return Principal{Name: "fake", Scope: ScopeRead}, active, nil
		})
	})
	r := httptest.NewRequest("GET", "http://127.0.0.1:7444/api/v1/stream", nil)
	r.Header.Set("Authorization", "Bearer fake-read")
	w := &streamWriter{headers: http.Header{}, frames: make(chan string, 10)}
	done := make(chan struct{})
	go func() { defer close(done); s.ServeHTTP(w, r) }()
	receiveFrame(t, w.frames)
	active = false
	ticker.ticks <- time.Time{}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("revoked token kept receiving live data")
	}
}

func TestRevealReachesOnlyPagesOpenOnThisComputer(t *testing.T) {
	s := serverFixture(t, func(opts *ServerOptions) {
		opts.StreamClock = fakeStreamClock{&fakeStreamTicker{ticks: make(chan time.Time, 1)}}
	})
	local := httptest.NewRequest("GET", "http://127.0.0.1:7444/", nil)
	local.RemoteAddr = "127.0.0.1:1234"
	session, ok, err := s.options.Auth.LocalSession(local)
	if err != nil || !ok {
		t.Fatalf("local session: %t %v", ok, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	open := func(token, peer string) *streamWriter {
		r := httptest.NewRequest("GET", "http://127.0.0.1:7444/api/v1/stream", nil).WithContext(ctx)
		r.RemoteAddr = peer
		r.Header.Set("Authorization", "Bearer "+token)
		w := &streamWriter{headers: http.Header{}, frames: make(chan string, 10)}
		go s.ServeHTTP(w, r)
		if frame := receiveFrame(t, w.frames); frame != ": connected\n\n" {
			t.Fatalf("handshake %q", frame)
		}
		return w
	}
	here := open(session, "127.0.0.1:1234")
	saved := open("fake-read", "127.0.0.1:1235")
	colleague := open("fake-read", "192.0.2.1:1234")
	for _, body := range []string{`{"hash":"javascript:x"}`, `{"nonce":"zz"}`, `{"hash":"#/boards/` + strings.Repeat("x", 300) + `"}`} {
		if w := apiRequest(s, "POST", "/api/v1/pages/reveal", "fake-read", []byte(body)); w.Code != 400 {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := apiRequest(s, "POST", "/api/v1/pages/reveal", "fake-read", []byte(`{"hash":"#/boards/main","nonce":"a5f0"}`)); w.Code != 200 {
		t.Fatalf("reveal: %d %s", w.Code, w.Body.String())
	}
	if frame := receiveFrame(t, here.frames); !strings.Contains(frame, "event: reveal\n") || !strings.Contains(frame, `"nonce":"a5f0"`) {
		t.Fatalf("local page frame %q", frame)
	}
	// The colleague's page gets the next ordinary event, never the reveal.
	if frame := receiveFrame(t, saved.frames); !strings.Contains(frame, "event: reveal\n") {
		t.Fatalf("local saved-token page frame %q", frame)
	}
	if err := s.options.Broker.Publish("alert", map[string]string{"value": "next"}); err != nil {
		t.Fatal(err)
	}
	if frame := receiveFrame(t, colleague.frames); !strings.Contains(frame, "event: alert\n") {
		t.Fatalf("remote page frame %q", frame)
	}
}

func TestRevealBoardOpensOnceUntilConnectedAndReopensAfterClose(t *testing.T) {
	b := NewBroker()
	now := time.Unix(100, 0)
	open := func(hash string, want bool) {
		t.Helper()
		reused, err := b.RevealBoard(RevealInput{Hash: hash, Nonce: "a5f0"}, now)
		if err != nil || reused != want {
			t.Fatalf("reveal %s: reused=%t err=%v", hash, reused, err)
		}
	}
	start, stop := b.subscribe(false, now)
	defer stop()
	open("#/", false)
	open("#/alerts", true)
	open("#/settings", true)
	board, closeBoard := b.subscribe(true, now)
	check := func(hash string) {
		t.Helper()
		select {
		case event := <-board:
			if !strings.Contains(string(event.Data), hash) {
				t.Fatalf("wrong route: %s", event.Data)
			}
		default:
			t.Fatal("board did not receive route")
		}
	}
	check("#/settings")
	open("#/boards/main", true)
	check("#/boards/main")
	if len(start) != 0 {
		t.Fatal("launcher took the board request")
	}
	closeBoard()
	open("#/alerts", false)
	now = now.Add(time.Minute)
	open("#/settings", false)
}
