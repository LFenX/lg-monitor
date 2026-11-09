//go:build integration && windows

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/store"
	collectorpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func TestNativeMonitorExportsSameFixtureOverHTTPAndOTLPAndStopsOwnWorker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wire := make(chan *collectorpb.ExportMetricsServiceRequest, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		var request collectorpb.ExportMetricsServiceRequest
		if err != nil || proto.Unmarshal(body, &request) != nil {
			t.Error("native OTLP wire invalid", err)
			w.WriteHeader(400)
			return
		}
		wire <- &request
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer receiver.Close()
	home := t.TempDir()
	configDir := filepath.Join(home, "config")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	settings := config.Settings{HTTPListen: "127.0.0.1:0", Exports: config.ExportSettings{Prometheus: config.PrometheusSettings{Enabled: true}, OTLP: config.OTLPSettings{Enabled: true, Endpoint: receiver.URL + "/v1/metrics"}}}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "settings.yaml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "targets.yaml"), []byte("targets: [{id: fake-web, collector: fake-node, match: {process: fake.exe}}]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "data", "lgmon.db")
	db, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Now().Add(-time.Second).UnixMilli()
	if err := db.Write(ctx, pipeline.Message{CollectorID: "fake-node", Seq: 1, TS: observed, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "fake-web", State: collector.StateDown}}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- RunLocal(ctx, LocalOptions{Home: home, Role: "monitor", Listen: "127.0.0.1:0", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), HTTPReady: func(address string) { ready <- address }, Notice: func(string) error { return nil }, NoTray: true})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error("native exporter runtime cleanup", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("native exporter runtime retained resources")
		}
	})
	address := receive(t, ready)
	base := "http://" + address
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	response, err := client.Get(base + "/api/v1/session")
	if err != nil {
		t.Fatal(err)
	}
	var session api.SessionResult
	decodeErr := json.NewDecoder(response.Body).Decode(&session)
	response.Body.Close()
	if decodeErr != nil || response.StatusCode != 200 || session.Scope != api.ScopeRead {
		t.Fatal("no native read session", response.StatusCode, decodeErr)
	}
	read := func(path string) ([]byte, string) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, "GET", base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+session.Token)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			t.Fatal("native exporter HTTP", path, response.StatusCode, err, string(body))
		}
		return body, response.Header.Get("Content-Type")
	}
	text, media := read("/metrics")
	if media != api.PrometheusContentType || !bytes.Contains(text, []byte("lgmon_target_state{collector_id=\"fake-node\",target_id=\"fake-web\"} 0\n")) {
		t.Fatal("native Prometheus not wired", media)
	}
	jsonBody, _ := read("/api/v1/exports/metrics")
	var snapshot store.ExportMetricSnapshot
	if err := json.Unmarshal(jsonBody, &snapshot); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, family := range snapshot.Families {
		for _, point := range family.Points {
			if family.Name == "lgmon_target_state" && point.Labels["collector_id"] == "fake-node" {
				found = point.Value == 0 && point.TS == observed
			}
		}
	}
	if !found {
		t.Fatal("native JSON did not read committed fixture")
	}
	request := receive(t, wire)
	found = false
	for _, r := range request.ResourceMetrics {
		for _, scope := range r.ScopeMetrics {
			for _, metric := range scope.Metrics {
				for _, point := range metric.GetGauge().DataPoints {
					if metric.Name == "lgmon_target_state" && point.TimeUnixNano == uint64(observed)*1000000 && point.GetAsDouble() == 0 {
						for _, attribute := range point.Attributes {
							if attribute.Key == "collector_id" && attribute.Value.GetStringValue() == "fake-node" {
								found = true
							}
						}
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("production monitor did not run the OTLP worker on the same observation")
	}
}
