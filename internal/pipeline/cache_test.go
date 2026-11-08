package pipeline

import (
	"math"
	"strings"
	"testing"
)

func TestCacheProjectionReservesWALAndRejectsInvalidTelemetry(t *testing.T) {
	rate := 20000000.0
	usage, err := NewCacheUsage(1000, 500000000, &rate)
	if err != nil || usage.ProjectedHours == nil || *usage.ProjectedHours != float64(500000000-CacheWALReserve)/rate || usage.Warning() != "" {
		t.Fatalf("projection: %+v %v", usage, err)
	}
	shortRate := 50000000.0
	short, err := NewCacheUsage(0, 500000000, &shortRate)
	if err != nil || !strings.Contains(short.Warning(), "9.8 小时") {
		t.Fatalf("capacity warning: %+v %v", short, err)
	}
	for _, invalid := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := NewCacheUsage(0, 500000000, &invalid); err == nil {
			t.Errorf("invalid cache rate accepted: %v", invalid)
		}
	}
	if _, err := NewCacheUsage(-1, 500000000, nil); err == nil {
		t.Fatal("negative cache bytes accepted")
	}
	if _, err := NewCacheUsage(0, CacheWALReserve, nil); err == nil {
		t.Fatal("cache limit without data budget accepted")
	}
}
