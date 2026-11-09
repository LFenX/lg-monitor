package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/store"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

type otlpTestSecrets map[string][]byte

func (s otlpTestSecrets) Get(_ context.Context, key string) ([]byte, error) {
	value, ok := s[key]
	if !ok {
		return nil, errors.New("fake secret missing")
	}
	return bytes.Clone(value), nil
}

type otlpTestWaiter struct{ delays []time.Duration }

func (w *otlpTestWaiter) Wait(ctx context.Context, d time.Duration) error {
	w.delays = append(w.delays, d)
	return ctx.Err()
}

func otlpTestSnapshot() store.ExportMetricSnapshot {
	return store.ExportMetricSnapshot{TS: 3000, Families: []store.ExportMetricFamily{
		{Name: "lgmon_collector_info", Help: "Collector identity.", Unit: "1", Points: []store.ExportMetricPoint{{Labels: map[string]string{"collector_id": "fake-node", "hostname": "虚构主机", "version": "fake-v1"}, Value: 1, TS: 3000}}},
		{Name: "lgmon_target_cpu_ratio", Help: "Whole-machine CPU ratio.", Unit: "1", Points: []store.ExportMetricPoint{{Labels: map[string]string{"collector_id": "fake-node", "target_id": "fake-web"}, Value: .25, TS: 2000}}},
	}}
}

func TestOTLPActualHTTPUsesOfficialProtobufAndPreservesResourceTimeUnits(t *testing.T) {
	requests := make(chan *collectorpb.ExportMetricsServiceRequest, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/metrics" || r.Header.Get("Content-Type") != "application/x-protobuf" || r.Header.Get("Authorization") != "Bearer fake-otlp-secret" {
			t.Error("not an authenticated OTLP metrics request", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(r.Body)
		var decoded collectorpb.ExportMetricsServiceRequest
		if err != nil || proto.Unmarshal(body, &decoded) != nil {
			t.Error("official decoder rejected request", err)
			w.WriteHeader(400)
			return
		}
		requests <- &decoded
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(200)
	}))
	defer server.Close()
	settings := config.OTLPSettings{Endpoint: server.URL + "/v1/metrics", AuthorizationRef: "fake-header"}
	client, header, err := newOTLPHTTPClient(context.Background(), settings, otlpTestSecrets{"fake-header": []byte("Bearer fake-otlp-secret")}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	batches, err := encodeOTLP(context.Background(), otlpTestSnapshot(), "fake-monitor", "fake-export-version")
	if err != nil || len(batches) != 1 {
		t.Fatal(len(batches), err)
	}
	waiter := &otlpTestWaiter{}
	if n, partial, err := sendOTLP(context.Background(), client, settings.Endpoint, header, batches[0], time.Now, waiter, func(d time.Duration) time.Duration { return d }); err != nil || partial || n != 0 {
		t.Fatal(n, partial, err)
	}
	wire := receive(t, requests)
	if len(wire.ResourceMetrics) != 1 || len(wire.ResourceMetrics[0].ScopeMetrics) != 1 {
		t.Fatal("resource grouping changed", wire)
	}
	attributes := map[string]string{}
	for _, attribute := range wire.ResourceMetrics[0].Resource.Attributes {
		attributes[attribute.Key] = attribute.Value.GetStringValue()
	}
	if attributes["service.instance.id"] != "fake-node" || attributes["host.name"] != "虚构主机" || attributes["lgmon.monitor.id"] != "fake-monitor" {
		t.Fatal("missing actual host identity", attributes)
	}
	metrics := wire.ResourceMetrics[0].ScopeMetrics[0].Metrics
	if len(metrics) != 2 || metrics[1].Name != "lgmon_target_cpu_ratio" || metrics[1].Unit != "1" || metrics[1].GetGauge().DataPoints[0].GetAsDouble() != .25 || metrics[1].GetGauge().DataPoints[0].TimeUnixNano != 2000000000 || len(waiter.delays) != 0 {
		t.Fatal("wrong metric, time, unit, or unexpected retry", metrics, waiter.delays)
	}
}

func TestOTLPResponseRetryPartialLimitsAndRedirectIsolation(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer other.Close()
	for _, kind := range []string{"retry503", "retry429", "partial", "permanent400", "permanent500", "invalid", "oversized", "gzip-oversized", "redirect"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt := calls.Add(1)
				w.Header().Set("Content-Type", "application/x-protobuf")
				switch kind {
				case "retry503", "retry429":
					if attempt == 1 {
						w.Header().Set("Retry-After", "2")
						if kind == "retry429" {
							w.WriteHeader(429)
						} else {
							w.WriteHeader(503)
						}
						return
					}
				case "permanent400":
					w.WriteHeader(400)
					return
				case "permanent500":
					w.WriteHeader(500)
					return
				case "invalid":
					_, _ = w.Write([]byte{255})
					return
				case "oversized":
					_, _ = w.Write(bytes.Repeat([]byte{0}, maxOTLPResponseBytes+1))
					return
				case "gzip-oversized":
					w.Header().Set("Content-Encoding", "gzip")
					compressed := gzip.NewWriter(w)
					_, _ = compressed.Write(bytes.Repeat([]byte{0}, maxOTLPResponseBytes+1))
					_ = compressed.Close()
					return
				case "redirect":
					w.Header().Set("Location", other.URL)
					w.WriteHeader(307)
					return
				case "partial":
					body, _ := proto.Marshal(&collectorpb.ExportMetricsServiceResponse{PartialSuccess: &collectorpb.ExportMetricsPartialSuccess{RejectedDataPoints: 2, ErrorMessage: "fake secret receiver echo must not be logged"}})
					_, _ = w.Write(body)
					return
				}
			}))
			defer server.Close()
			settings := config.OTLPSettings{Endpoint: server.URL + "/v1/metrics"}
			client, header, err := newOTLPHTTPClient(context.Background(), settings, nil, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			waiter := &otlpTestWaiter{}
			rejected, partial, err := sendOTLP(context.Background(), client, settings.Endpoint, header, []byte{}, time.Now, waiter, func(d time.Duration) time.Duration { return d })
			if strings.HasPrefix(kind, "retry") {
				if err != nil || calls.Load() != 2 || len(waiter.delays) != 1 || waiter.delays[0] < 2*time.Second {
					t.Fatal("retry/throttling ignored", calls.Load(), waiter.delays, err)
				}
			} else if kind == "partial" {
				if err != nil || !partial || rejected != 2 || calls.Load() != 1 || len(waiter.delays) != 0 {
					t.Fatal("partial batch retried", rejected, partial, err, calls.Load())
				}
			} else if err == nil || calls.Load() != 1 || len(waiter.delays) != 0 {
				t.Fatal("permanent or unsafe response retried", kind, err, calls.Load())
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("OTLP followed redirect with exporter credentials")
	}
}

func TestOTLPTLSAndMutualTLSUseOnlyExplicitTrustAndClientKeyRefs(t *testing.T) {
	now := time.Now()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake-client"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(parsed)
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) != 1 {
			t.Error("missing verified client identity")
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientRoots}
	server.Config.ErrorLog = slog.NewLogLogger(slog.NewTextHandler(io.Discard, nil), slog.LevelError)
	server.StartTLS()
	defer server.Close()
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	secrets := otlpTestSecrets{"fake-ca": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), "fake-cert": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), "fake-key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})}
	settings := config.OTLPSettings{Endpoint: server.URL + "/v1/metrics", TLS: config.ExportTLS{CARef: "fake-ca", CertRef: "fake-cert", KeyRef: "fake-key"}}
	client, header, err := newOTLPHTTPClient(context.Background(), settings, secrets, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if _, _, err := sendOTLP(context.Background(), client, settings.Endpoint, header, nil, time.Now, &otlpTestWaiter{}, func(d time.Duration) time.Duration { return d }); err != nil || requests.Load() != 1 {
		t.Fatal("valid mTLS failed", err, requests.Load())
	}
	settings.TLS.CARef = ""
	untrusted, header, err := newOTLPHTTPClient(context.Background(), settings, secrets, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer untrusted.CloseIdleConnections()
	if _, _, err := sendOTLP(context.Background(), untrusted, settings.Endpoint, header, nil, time.Now, &otlpTestWaiter{}, func(d time.Duration) time.Duration { return d }); err == nil || requests.Load() != 1 {
		t.Fatal("untrusted server accepted", err, requests.Load())
	}
}

type otlpSnapshotFunc func(context.Context, []config.Target) (store.ExportMetricSnapshot, error)

func (f otlpSnapshotFunc) ExportMetrics(ctx context.Context, targets []config.Target) (store.ExportMetricSnapshot, error) {
	return f(ctx, targets)
}

type cancelOTLPClient struct {
	requests        chan *http.Request
	closed          atomic.Int32
	active, maximum *atomic.Int32
}

func (c *cancelOTLPClient) CloseIdleConnections() { c.closed.Add(1) }
func (c *cancelOTLPClient) Do(request *http.Request) (*http.Response, error) {
	active := c.active.Add(1)
	defer c.active.Add(-1)
	for old := c.maximum.Load(); active > old && !c.maximum.CompareAndSwap(old, active); old = c.maximum.Load() {
	}
	c.requests <- request
	<-request.Context().Done()
	return nil, request.Context().Err()
}

func TestOTLPConfigChangeCancelsOldRequestBeforeStartingNewAndDisableReleases(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager, err := config.NewManager(config.FSLoader{FS: fstest.MapFS{"settings.yaml": {Data: []byte("exports:\n  otlp: {enabled: true, endpoint: https://fake.example/v1/metrics}\n")}}}, logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var active, maximum atomic.Int32
	clients := make(chan *cancelOTLPClient, 4)
	clock := &fakeClock{created: make(chan *fakeTicker, 4)}
	opts := OTLPExportOptions{Config: manager, Source: otlpSnapshotFunc(func(context.Context, []config.Target) (store.ExportMetricSnapshot, error) {
		return otlpTestSnapshot(), nil
	}), MonitorID: "fake-monitor", Now: time.Now, Clock: clock, Logger: logger, NewClient: func(context.Context, config.OTLPSettings) (OTLPHTTPClient, string, error) {
		c := &cancelOTLPClient{requests: make(chan *http.Request, 1), active: &active, maximum: &maximum}
		clients <- c
		return c, "", nil
	}}
	done := make(chan error, 1)
	go func() { done <- RunOTLPExports(ctx, opts) }()
	defer func() {
		cancel()
		if err := receive(t, done); err != nil {
			t.Error(err)
		}
	}()
	first := receive(t, clients)
	receive(t, first.requests)
	updated := *manager.Snapshot()
	updated.Settings.Exports.OTLP.Endpoint = "https://other.example/v1/metrics"
	if err := manager.Apply(&updated); err != nil {
		t.Fatal(err)
	}
	second := receive(t, clients)
	receive(t, second.requests)
	if first.closed.Load() != 1 || maximum.Load() != 1 {
		t.Fatal("old exporter retained resources or overlapped", first.closed.Load(), maximum.Load())
	}
	updated = *manager.Snapshot()
	updated.Settings.Exports.OTLP.Enabled = false
	if err := manager.Apply(&updated); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for second.closed.Load() != 1 {
		select {
		case <-deadline.C:
			t.Fatal("disabled exporter did not release HTTP resources")
		case <-time.After(time.Millisecond):
		}
	}
	if active.Load() != 0 {
		t.Fatal("disabled exporter retained active request")
	}
}

func TestOTLPRetryAfterStaysWithinTheParentExportBudget(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	}))
	defer server.Close()
	settings := config.OTLPSettings{Endpoint: server.URL + "/v1/metrics"}
	client, header, err := newOTLPHTTPClient(context.Background(), settings, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err = sendOTLP(ctx, client, settings.Endpoint, header, nil, time.Now, nativeOTLPWaiter{}, func(d time.Duration) time.Duration { return d })
	var failure *otlpExportError
	if !errors.As(err, &failure) || failure.class != "timeout" || !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatal("retry exceeded budget or ignored server delay", err, calls.Load())
	}
}
