package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

func exportFixture() store.ExportMetricSnapshot {
	return store.ExportMetricSnapshot{TS: 2000, Families: []store.ExportMetricFamily{{Name: "lgmon_target_state", Help: "Latest target state.", Unit: "1", Points: []store.ExportMetricPoint{{Labels: map[string]string{"collector_id": "fake-host", "target_id": "fake\"目标\\\n"}, Value: -1, TS: 1500}}}}}
}

func TestPrometheusUsesOfficialParserAndRealMediaType(t *testing.T) {
	snapshot := exportFixture()
	body, err := EncodePrometheus(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(string(body)))
	if err != nil {
		t.Fatal("official Prometheus parser rejected wire format", err, string(body))
	}
	got := families["lgmon_target_state"].GetMetric()
	if len(got) != 1 || got[0].GetGauge().GetValue() != -1 || got[0].GetTimestampMs() != 0 || got[0].GetLabel()[1].GetValue() != snapshot.Families[0].Points[0].Labels["target_id"] {
		t.Fatal("wire changed identity or invented health/time", got)
	}
	s := serverFixture(t, func(opts *ServerOptions) {
		opts.Actions[PrometheusMetrics] = func(context.Context, Request) (any, error) { return snapshot, nil }
	})
	response := apiRequest(s, "GET", "/metrics", "fake-read", nil)
	if response.Code != 200 || response.Header().Get("Content-Type") != PrometheusContentType || response.Body.String() != string(body) {
		t.Fatal("HTTP returned a JSON wrapper or wrong format", response.Code, response.Header(), response.Body.String())
	}
	for _, tc := range []struct {
		method, token string
		status        int
	}{{"GET", "", 401}, {"GET", "invalid", 401}, {"POST", "fake-read", 405}} {
		if got := apiRequest(s, tc.method, "/metrics", tc.token, nil); got.Code != tc.status {
			t.Fatal(tc, got.Code)
		}
	}
	spec := apiRequest(s, "GET", "/api/v1/openapi.json", "fake-read", nil)
	var document map[string]any
	if err := json.Unmarshal(spec.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	responses := document["paths"].(map[string]any)["/metrics"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)
	if responses["text/plain"] == nil || responses["application/json"] != nil {
		t.Fatal("OpenAPI misrepresented Prometheus", responses)
	}
}

func TestMetricWireRejectsNonFiniteDuplicateSensitiveOrInvalidData(t *testing.T) {
	for _, kind := range []string{"nan", "duplicate", "commandline", "utf8", "timestamp", "name"} {
		snapshot := exportFixture()
		switch kind {
		case "nan":
			snapshot.Families[0].Points[0].Value = math.NaN()
		case "duplicate":
			snapshot.Families[0].Points = append(snapshot.Families[0].Points, snapshot.Families[0].Points[0])
		case "commandline":
			snapshot.Families[0].Points[0].Labels["cmdline"] = "fake-secret-never-expose"
		case "utf8":
			snapshot.Families[0].Points[0].Labels["target_id"] = string([]byte{255})
		case "timestamp":
			snapshot.Families[0].Points[0].TS = -1
		case "name":
			snapshot.Families[0].Name = "unsafe\nmetric"
		}
		if body, err := EncodePrometheus(snapshot); err == nil || len(body) != 0 {
			t.Fatal("invalid data partly exported", kind, string(body), err)
		}
	}
}

func TestPrometheusOversizedOutputFailsWithoutReturningPartialMetrics(t *testing.T) {
	snapshot := exportFixture()
	snapshot.Families[0].Points = nil
	large := strings.Repeat("x", 4096)
	for i := 0; i < 1100; i++ {
		snapshot.Families[0].Points = append(snapshot.Families[0].Points, store.ExportMetricPoint{Labels: map[string]string{"collector_id": "fake-node", "target_id": fmt.Sprintf("fake-%04d", i), "hostname": large, "version": large, "volume": large, "interface": large}, Value: 1, TS: 1000})
	}
	if body, err := EncodePrometheus(snapshot); err == nil || len(body) != 0 {
		t.Fatal("oversized result partially exported", len(body), err)
	}
}
