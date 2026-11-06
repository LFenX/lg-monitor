package collector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

func httpProbe(url string) config.Probe {
	return config.Probe{Type: "http", URL: url, Interval: "1s", Timeout: "1s"}
}

func TestHTTPProbeStatus(t *testing.T) {
	for _, tc := range []struct {
		code     int
		expected config.Status
		ok       bool
	}{
		{200, "", true}, {204, "2xx", true}, {404, "404", true}, {499, "<500", true},
		{500, "<500", false}, {302, "2xx", false}, {200, ">=200", true}, {299, "<=299", true}, {200, ">200", false},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.code) }))
		prober := NewProber(time.Now)
		probe := httpProbe(server.URL)
		probe.ExpectStatus = tc.expected
		result := prober.Execute(context.Background(), "demo", 2, probe)
		prober.Close()
		server.Close()
		if result.OK != tc.ok || result.StatusCode != tc.code || result.TargetID != "demo" || result.Probe != 2 || result.TS <= 0 || result.LatencyMS < 0 {
			t.Fatalf("status %d expected %s: %+v", tc.code, tc.expected, result)
		}
		if (result.Error == "") != tc.ok {
			t.Fatalf("inconsistent error for status %d: %+v", tc.code, result)
		}
	}
}

func TestHTTPProbeDoesNotRedirect(t *testing.T) {
	var followed atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Add(1) }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer server.Close()
	prober := NewProber(time.Now)
	defer prober.Close()
	probe := httpProbe(server.URL)
	probe.ExpectStatus = "302"
	result := prober.Execute(context.Background(), "redirect", 0, probe)
	if !result.OK || result.StatusCode != 302 || followed.Load() != 0 {
		t.Fatalf("redirect was followed: %+v, requests %d", result, followed.Load())
	}
}

type routingDialer struct {
	destination string
	mu          sync.Mutex
	addresses   []string
}

func (d *routingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.addresses = append(d.addresses, address)
	d.mu.Unlock()
	return (&net.Dialer{}).DialContext(ctx, network, d.destination)
}

func TestHTTPProbeDoesNotUseProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy.invalid:9191")
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:9191")
	t.Setenv("NO_PROXY", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	dialer := &routingDialer{destination: server.Listener.Addr().String()}
	prober := newProber(nil, dialer, time.Now)
	defer prober.Close()
	result := prober.Execute(context.Background(), "direct", 0, httpProbe("http://probe.invalid/check"))
	dialer.mu.Lock()
	defer dialer.mu.Unlock()
	if !result.OK || len(dialer.addresses) != 1 || dialer.addresses[0] != "probe.invalid:80" {
		t.Fatalf("system proxy affected the request: %+v, addresses %v", result, dialer.addresses)
	}
}

func TestHTTPProbeTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	prober := NewProber(time.Now)
	defer prober.Close()
	probe := httpProbe(server.URL)
	probe.Timeout = "20ms"
	result := prober.Execute(context.Background(), "timeout", 0, probe)
	if result.OK || !strings.Contains(result.Error, "超时") || !errors.Is(result.cause, context.DeadlineExceeded) {
		t.Fatalf("timeout was not recorded: %+v", result)
	}
}

func TestHTTPProbeConnectionRefused(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()
	prober := NewProber(time.Now)
	defer prober.Close()
	result := prober.Execute(context.Background(), "closed-fixture", 0, httpProbe(address))
	var connectionError *net.OpError
	if result.OK || result.StatusCode != 0 || !errors.As(result.cause, &connectionError) {
		t.Fatalf("closed httptest server did not produce a connection failure: %+v", result)
	}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

type dialerFunc func(context.Context, string, string) (net.Conn, error)

func (f dialerFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return f(ctx, network, address)
}

func TestProbeFailureAndSafeError(t *testing.T) {
	const sensitive = "fake-secret-do-not-persist"
	clock := time.UnixMilli(1000)
	failure := errors.New("connection refused for http://test.invalid/?token=" + sensitive)
	prober := newProber(doerFunc(func(*http.Request) (*http.Response, error) {
		clock = clock.Add(13 * time.Millisecond)
		return nil, failure
	}), nil, func() time.Time { return clock })
	result := prober.Execute(context.Background(), "failed", 3, httpProbe("http://test.invalid/"))
	encoded, err := json.Marshal(result)
	if err != nil || result.OK || result.Error == "" || result.LatencyMS != 13 || result.TS != 1000 || !errors.Is(result.cause, failure) || strings.Contains(string(encoded), sensitive) {
		t.Fatalf("unsafe or incomplete failed result: %s, %v", encoded, err)
	}
	refused := newProber(nil, dialerFunc(func(context.Context, string, string) (net.Conn, error) { return nil, failure }), time.Now)
	result = refused.Execute(context.Background(), "tcp", 0, config.Probe{Type: "tcp", Address: "127.0.0.1:1", Timeout: "1s"})
	if result.OK || !strings.Contains(result.Error, "TCP") || !errors.Is(result.cause, failure) {
		t.Fatalf("TCP refusal not recorded: %+v", result)
	}
	prober.Close()
	refused.Close()
}

type closeTrackingBody struct {
	io.Reader
	closed bool
}

func (b *closeTrackingBody) Close() error { b.closed = true; return nil }

func TestHTTPProbeClosesResponse(t *testing.T) {
	body := &closeTrackingBody{Reader: strings.NewReader("response")}
	prober := newProber(doerFunc(func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: 200, Body: body}, nil }), nil, time.Now)
	result := prober.Execute(context.Background(), "demo", 0, httpProbe("http://test.invalid/"))
	if !result.OK || !body.closed {
		t.Fatalf("response leaked: %+v, closed %v", result, body.closed)
	}
}

func TestTCPProbeOnOwnListener(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	prober := NewProber(time.Now)
	defer prober.Close()
	result := prober.Execute(context.Background(), "tcp", 1, config.Probe{Type: "tcp", Address: listener.Addr().String(), Timeout: "1s"})
	if !result.OK || result.Error != "" || result.Probe != 1 {
		t.Fatalf("own TCP listener was not reachable: %+v", result)
	}
}
