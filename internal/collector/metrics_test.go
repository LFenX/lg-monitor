package collector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeMetricsBackend struct {
	value rawSystemMetrics
	err   error
}

func (b *fakeMetricsBackend) Read(context.Context) (rawSystemMetrics, error) { return b.value, b.err }

func TestSystemMetricsUseCPUAndNetworkCounterDeltas(t *testing.T) {
	now := time.UnixMilli(1000)
	backend := &fakeMetricsBackend{value: rawSystemMetrics{cpuTotal: 100, cpuIdle: 40, SystemMetrics: SystemMetrics{MemoryTotal: 1000, MemoryUsed: 300, MemoryAvailable: 700, MemoryPercent: 30, Networks: []NetworkMetrics{{Name: "fake", BytesSent: 100, BytesReceived: 200}}}}}
	source := newMetricsSource(backend, func() time.Time { return now }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	first, err := source.Snapshot(context.Background())
	if err != nil || first.CPUPercent != 0 || first.Networks[0].SendBytesPerSec != 0 {
		t.Fatalf("first metric sample: %+v %v", first, err)
	}
	first.Networks[0].BytesSent = 999999
	now = now.Add(2 * time.Second)
	backend.value.cpuTotal, backend.value.cpuIdle = 108, 42
	backend.value.Networks[0].BytesSent, backend.value.Networks[0].BytesReceived = 200, 500
	next, err := source.Snapshot(context.Background())
	if err != nil || next.CPUPercent != 75 || next.Networks[0].SendBytesPerSec != 50 || next.Networks[0].ReceiveBytesPerSec != 150 {
		t.Fatalf("counter deltas or snapshot isolation: %+v %v", next, err)
	}
	now = now.Add(2 * time.Second)
	backend.value.cpuTotal, backend.value.cpuIdle = 1, 0
	backend.value.Networks[0].BytesSent, backend.value.Networks[0].BytesReceived = 0, 0
	reset, err := source.Snapshot(context.Background())
	if err != nil || reset.CPUPercent != 0 || reset.Networks[0].ReceiveBytesPerSec != 0 {
		t.Fatalf("counter reset became negative/spiked: %+v %v", reset, err)
	}
	backend.err = errors.New("fake source failure")
	if _, err := source.Snapshot(context.Background()); !errors.Is(err, backend.err) {
		t.Fatalf("metric failure hidden: %v", err)
	}
}
