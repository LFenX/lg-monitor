package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/store"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

type otlpLogBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *otlpLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}
func (b *otlpLogBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.data.String() }

type observedOTLPConfig struct {
	*config.Manager
	subscribed chan struct{}
}

func (m observedOTLPConfig) Subscribe(ctx context.Context) <-chan *config.Config {
	updates := m.Manager.Subscribe(ctx)
	close(m.subscribed)
	return updates
}

type successOTLPClient struct {
	requests chan *http.Request
	closed   chan struct{}
}

func (c *successOTLPClient) Do(r *http.Request) (*http.Response, error) {
	c.requests <- r
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/x-protobuf"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
}
func (c *successOTLPClient) CloseIdleConnections() { c.closed <- struct{}{} }

func TestOTLPDefaultDisabledThenPeriodicFailureRecoveryUsesLatestTargets(t *testing.T) {
	var logs otlpLogBuffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	manager, err := config.NewManager(config.FSLoader{FS: fstest.MapFS{}}, logger)
	if err != nil {
		t.Fatal(err)
	}
	observed := observedOTLPConfig{Manager: manager, subscribed: make(chan struct{})}
	clock := &fakeClock{created: make(chan *fakeTicker, 4)}
	client := &successOTLPClient{requests: make(chan *http.Request, 4), closed: make(chan struct{}, 4)}
	var reads, clients atomic.Int32
	gotTargets := make(chan []config.Target, 4)
	opts := OTLPExportOptions{Config: observed, Source: otlpSnapshotFunc(func(_ context.Context, targets []config.Target) (store.ExportMetricSnapshot, error) {
		gotTargets <- targets
		if reads.Add(1) == 1 {
			return store.ExportMetricSnapshot{}, errors.New("fake-sensitive-source-error")
		}
		return otlpTestSnapshot(), nil
	}), Now: time.Now, Clock: clock, Logger: logger, NewClient: func(context.Context, config.OTLPSettings) (OTLPHTTPClient, string, error) {
		clients.Add(1)
		return client, "", nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunOTLPExports(ctx, opts) }()
	defer func() {
		cancel()
		if err := receive(t, done); err != nil {
			t.Error(err)
		}
	}()
	receive(t, observed.subscribed)
	select {
	case <-client.closed:
		t.Fatal("disabled exporter initialized HTTP client")
	case <-time.After(20 * time.Millisecond):
	}
	if clients.Load() != 0 || reads.Load() != 0 || len(clock.created) != 0 {
		t.Fatal("default settings performed exporter work")
	}
	cfg := *manager.Snapshot()
	cfg.Settings.Exports.OTLP = config.OTLPSettings{Enabled: true, Endpoint: "https://fake.example/v1/metrics"}
	if err := manager.Apply(&cfg); err != nil {
		t.Fatal(err)
	}
	ticker := receive(t, clock.created)
	receive(t, gotTargets)
	receive(t, client.closed)
	if len(client.requests) != 0 {
		t.Fatal("failed source was sent")
	}
	cfg = *manager.Snapshot()
	cfg.Targets = []config.Target{{ID: "new-target", Match: config.Match{Process: "fake.exe"}}}
	if err := manager.Apply(&cfg); err != nil {
		t.Fatal(err)
	}
	clock.tick(ticker, 30*time.Second)
	targets := receive(t, gotTargets)
	receive(t, client.requests)
	receive(t, client.closed)
	if len(targets) != 1 || targets[0].ID != "new-target" || clients.Load() != 2 {
		t.Fatal("periodic recovery used old config or leaked client", targets, clients.Load())
	}
	if text := logs.String(); strings.Contains(text, "fake-sensitive-source-error") || !strings.Contains(text, "class=source") {
		t.Fatal("source failure lost safe classification", text)
	}
}

func TestOTLPCredentialRotationAndPartialLogsNeverExposeReceiverEcho(t *testing.T) {
	headers := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Get("Authorization")
		body, _ := proto.Marshal(&collectorpb.ExportMetricsServiceResponse{PartialSuccess: &collectorpb.ExportMetricsPartialSuccess{RejectedDataPoints: 1, ErrorMessage: r.Header.Get("Authorization")}})
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	var logs otlpLogBuffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	manager, err := config.NewManager(config.FSLoader{FS: fstest.MapFS{}}, logger)
	if err != nil {
		t.Fatal(err)
	}
	secrets := otlpTestSecrets{"fake-header": []byte("Bearer fake-private-v1")}
	settings := config.OTLPSettings{Endpoint: server.URL + "/v1/metrics", AuthorizationRef: "fake-header"}
	opts := OTLPExportOptions{Config: manager, Source: otlpSnapshotFunc(func(context.Context, []config.Target) (store.ExportMetricSnapshot, error) {
		return otlpTestSnapshot(), nil
	}), Now: time.Now, Waiter: &otlpTestWaiter{}, Jitter: func(d time.Duration) time.Duration { return d }, Logger: logger, NewClient: func(ctx context.Context, s config.OTLPSettings) (OTLPHTTPClient, string, error) {
		return newOTLPHTTPClient(ctx, s, secrets, time.Now)
	}}
	for _, value := range []string{"Bearer fake-private-v1", "Bearer fake-private-v2"} {
		secrets["fake-header"] = []byte(value)
		if err := exportOTLPCycle(context.Background(), opts, settings); err != nil {
			t.Fatal(err)
		}
		if got := receive(t, headers); got != value {
			t.Fatal("secret contents were not refreshed", got)
		}
	}
	text := logs.String()
	if !strings.Contains(text, "rejected=1") || strings.Contains(text, "fake-private") || strings.Contains(text, server.URL) {
		t.Fatal("partial receiver echo or URL leaked", text)
	}
}

func TestOTLPBatchesKeepEveryPointAndCancellationStopsEncoding(t *testing.T) {
	snapshot := otlpTestSnapshot()
	snapshot.Families = snapshot.Families[1:]
	snapshot.Families[0].Name = "lgmon_script_value"
	snapshot.Families[0].Unit = ""
	snapshot.Families[0].Help = "Original script values."
	snapshot.Families[0].Points = nil
	for i := 0; i < 1201; i++ {
		snapshot.Families[0].Points = append(snapshot.Families[0].Points, store.ExportMetricPoint{Labels: map[string]string{"collector_id": "fake-node", "target_id": fmt.Sprintf("target-%02d", i%50), "probe": "0", "metric": fmt.Sprintf("metric-%02d", i/50)}, Value: float64(i), TS: 2000})
	}
	batches, err := encodeOTLP(context.Background(), snapshot, "fake-monitor", "fake-v1")
	if err != nil || len(batches) < 2 {
		t.Fatal("batching missing", len(batches), err)
	}
	values := map[float64]bool{}
	for _, body := range batches {
		if len(body) > maxOTLPRequestBytes {
			t.Fatal("unbounded request", len(body))
		}
		var message collectorpb.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &message); err != nil {
			t.Fatal(err)
		}
		for _, r := range message.ResourceMetrics {
			for _, scope := range r.ScopeMetrics {
				for _, metric := range scope.Metrics {
					for _, point := range metric.GetGauge().DataPoints {
						if values[point.GetAsDouble()] {
							t.Fatal("duplicate batch point")
						}
						values[point.GetAsDouble()] = true
						if point.TimeUnixNano != 2000000000 {
							t.Fatal("batch time changed")
						}
					}
				}
			}
		}
	}
	if len(values) != 1201 {
		t.Fatal("batch dropped metrics", len(values))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := encodeOTLP(ctx, snapshot, "fake-monitor", "fake-v1"); !errors.Is(err, context.Canceled) {
		t.Fatal("encoding ignored cancellation", err)
	}
}
