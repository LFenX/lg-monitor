// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/store"
)

type fakeMetricHistories struct{ calls []store.HistoryRange }

func (f *fakeMetricHistories) MetricHistory(_ context.Context, _, name string, period store.HistoryRange) (store.MetricHistoryResult, error) {
	f.calls = append(f.calls, period)
	if name == "system.swap_percent" {
		return store.MetricHistoryResult{}, nil
	}
	value := 40.0
	if name == "system.memory_percent" {
		value = 70
	}
	// Two samples in the first bucket weigh more than one, and one point falls in the last bucket.
	return store.MetricHistoryResult{Metrics: []store.MetricPoint{
		{TS: period.From, Samples: 2, Average: value},
		{TS: period.From + 1, Samples: 1, Average: value + 30},
		{TS: period.To, Samples: 1, Average: value},
	}}, nil
}

func TestDeviceHistoryBucketsEveryRangeForSync(t *testing.T) {
	source := &fakeMetricHistories{}
	now := time.UnixMilli(1795350000000)
	history, err := readDeviceHistory(context.Background(), source, "fake-host", now)
	if err != nil {
		t.Fatal(err)
	}
	spans := []string{}
	for _, series := range history.Series {
		spans = append(spans, series.Span)
		if series.CPU[0] != 50 || series.Memory[0] != 80 || series.CPU[len(series.CPU)-1] != 40 || series.CPU[1] != -1 || series.Swap != nil {
			t.Fatalf("%s: cpu=%v... mem=%v... swap=%v", series.Span, series.CPU[:2], series.Memory[:1], series.Swap)
		}
		if int64(len(series.CPU)-1)*series.Step > 7*24*time.Hour.Milliseconds()+series.Step {
			t.Fatalf("%s: %d buckets of %dms", series.Span, len(series.CPU), series.Step)
		}
	}
	if !slices.Equal(spans, []string{"1h", "6h", "24h", "7d"}) || len(source.calls) != 12 {
		t.Fatalf("spans=%v calls=%d", spans, len(source.calls))
	}
	if steps := []string{source.calls[0].Step, source.calls[3].Step, source.calls[9].Step}; !slices.Equal(steps, []string{"raw", "1m", "1h"}) {
		t.Fatalf("read resolutions=%v", steps)
	}
	// Four ranges of three curves stay a few kilobytes of the synced state.
	if data, _ := json.Marshal(history); len(data) > 12*1024 {
		t.Fatalf("history is %d bytes", len(data))
	}
}
