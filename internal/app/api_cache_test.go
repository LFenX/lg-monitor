package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestCollectorsHTTPReturnsActualCacheAndUnknownLegacy(t *testing.T) {
	f := newAPIFixture(t)
	rate := 25000000.0
	usage, err := pipeline.NewCacheUsage(1234567, 500000000, &rate)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Heartbeat(context.Background(), "fake-host", &usage); err != nil {
		t.Fatal(err)
	}
	response := f.request(t, "GET", "/api/v1/collectors", "read", "", 200)
	var rows []api.Collector
	if err := json.Unmarshal(response.Body.Bytes(), &rows); err != nil || len(rows) != 2 || rows[0].Cache == nil ||
		rows[0].Cache.Bytes != usage.Bytes || rows[0].Cache.MaxBytes != usage.MaxBytes ||
		rows[0].Cache.BytesPerHour == nil || *rows[0].Cache.BytesPerHour != rate ||
		rows[0].Cache.ProjectedHours == nil || *rows[0].Cache.ProjectedHours != *usage.ProjectedHours || rows[1].Cache != nil {
		t.Fatalf("HTTP cache: %s %v", response.Body.String(), err)
	}
}
