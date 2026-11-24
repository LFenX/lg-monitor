// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/store"
)

// deviceSpans are the device page's ranges, each read at the resolution this computer keeps for it and folded
// into buckets coarse enough that all four fit in a few kilobytes of the synced state.
var deviceSpans = []struct {
	span   string
	length time.Duration
	read   string
	bucket time.Duration
}{
	{"1h", time.Hour, "raw", time.Minute},
	{"6h", 6 * time.Hour, "1m", 5 * time.Minute},
	{"24h", 24 * time.Hour, "1m", 10 * time.Minute},
	{"7d", 7 * 24 * time.Hour, "1h", time.Hour},
}

// The state carrying the history goes out at most every five minutes, so reading it more often is wasted.
const deviceHistoryEvery = 5 * time.Minute

type deviceHistoryCache struct {
	mu     sync.Mutex
	at     time.Time
	values map[string]*api.DeviceHistory
}

type metricHistories interface {
	MetricHistory(context.Context, string, string, store.HistoryRange) (store.MetricHistoryResult, error)
}

// deviceHistories reads each computer's CPU, memory and swap curves for the other devices of the account.
func (b *APIBackend) deviceHistories(ctx context.Context, devices []api.Device) map[string]*api.DeviceHistory {
	cache := &b.deviceHistory
	now := b.options.Now()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.values != nil && now.Sub(cache.at) < deviceHistoryEvery && now.Sub(cache.at) >= 0 {
		return cache.values
	}
	values := map[string]*api.DeviceHistory{}
	for _, device := range devices {
		if device.System == nil {
			continue
		}
		history, err := readDeviceHistory(ctx, b.options.Read, device.ID, now)
		if err != nil {
			if ctx.Err() != nil {
				return cache.values
			}
			b.options.Logger.Warn("Device history for cloud sync failed", "collectorId", device.ID, "error", err)
			continue
		}
		values[device.ID] = history
	}
	cache.at, cache.values = now, values
	return values
}

func readDeviceHistory(ctx context.Context, source metricHistories, collector string, now time.Time) (*api.DeviceHistory, error) {
	history := &api.DeviceHistory{Series: []api.DeviceSeries{}}
	names := []string{"system.cpu_percent", "system.memory_percent", "system.swap_percent"}
	for _, span := range deviceSpans {
		step := span.bucket.Milliseconds()
		to := now.UnixMilli()
		from := (to - span.length.Milliseconds()) / step * step
		series := api.DeviceSeries{Span: span.span, From: from, Step: step}
		lists := make([][]float64, len(names))
		for index, name := range names {
			result, err := source.MetricHistory(ctx, collector, name, store.HistoryRange{From: from, To: to, Step: span.read})
			if err != nil {
				return nil, err
			}
			lists[index] = bucketPoints(result.Metrics, from, step, int((to-from)/step)+1)
		}
		series.CPU, series.Memory, series.Swap = lists[0], lists[1], lists[2]
		if allMissing(series.Swap) {
			series.Swap = nil
		}
		history.Series = append(history.Series, series)
	}
	return history, nil
}

// bucketPoints averages the points of each bucket by their sample counts; -1 marks a bucket with no data, which
// JSON cannot carry as NaN.
func bucketPoints(points []store.MetricPoint, from, step int64, count int) []float64 {
	sums, weights := make([]float64, count), make([]float64, count)
	for _, point := range points {
		index := int((point.TS - from) / step)
		if index < 0 || index >= count || math.IsNaN(point.Average) || math.IsInf(point.Average, 0) {
			continue
		}
		weight := float64(max(point.Samples, 1))
		sums[index] += point.Average * weight
		weights[index] += weight
	}
	result := make([]float64, count)
	for index := range result {
		result[index] = -1
		if weights[index] > 0 {
			result[index] = math.Round(sums[index]/weights[index]*10) / 10
		}
	}
	return result
}

func allMissing(values []float64) bool {
	for _, value := range values {
		if value >= 0 {
			return false
		}
	}
	return true
}
