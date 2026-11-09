package app

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestAPIExportUsesReadOnlyStoreAndPrometheusIsDisabledUntilEnabled(t *testing.T) {
	f := newAPIFixture(t)
	f.backend.options.Metrics = f.view
	f.samples(t)
	f.request(t, "GET", "/metrics", "read", "", 404)
	structured := f.request(t, "GET", "/api/v1/exports/metrics", "read", "", 200)
	var snapshot store.ExportMetricSnapshot
	if err := json.Unmarshal(structured.Body.Bytes(), &snapshot); err != nil || len(snapshot.Families) == 0 {
		t.Fatal("no persisted metrics", err, snapshot)
	}
	cfg := *f.manager.Snapshot()
	cfg.Settings.Exports.Prometheus.Enabled = true
	if err := f.manager.Apply(&cfg); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "http://127.0.0.1:7444/metrics", nil)
	request.RemoteAddr = "192.0.2.5:1234"
	request.Header.Set("Authorization", "Bearer fake-read")
	response := httptest.NewRecorder()
	f.server.ServeHTTP(response, request)
	want, err := api.EncodePrometheus(snapshot)
	if err != nil || response.Code != 200 || response.Header().Get("Content-Type") != api.PrometheusContentType || !bytes.Equal(response.Body.Bytes(), want) {
		t.Fatal("HTTP and persistent snapshot disagree", err, response.Code, response.Body.String())
	}
	if f.vault.setCalls != 0 || len(f.client.sent) != 0 || f.hookFactories != 0 {
		t.Fatal("read-only export touched credentials or notification senders")
	}
	f.backend.options.Metrics = nil
	f.request(t, "GET", "/api/v1/exports/metrics", "read", "", 503)
}
