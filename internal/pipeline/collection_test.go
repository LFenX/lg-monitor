package pipeline

import (
	"context"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
)

func TestChannelOwnsMetricsAndScriptCollections(t *testing.T) {
	channel := NewChannel(2)
	defer channel.Close()
	system := Message{CollectorID: "fake", Seq: 1, Kind: KindSystemMetrics, SystemMetrics: &collector.SystemMetrics{Disks: []collector.DiskMetrics{{Path: `C:\`}}, Networks: []collector.NetworkMetrics{{Name: "fake"}}, Errors: []string{"old"}}}
	script := Message{CollectorID: "fake", Seq: 2, Kind: KindProbeResult, ProbeResult: &ProbeResult{TargetID: "fake", Type: "script", Metrics: map[string]float64{"age": 2}}}
	for _, message := range []Message{system, script} {
		if err := channel.Write(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	system.SystemMetrics.Disks[0].Path = "changed"
	system.SystemMetrics.Networks[0].Name = "changed"
	system.SystemMetrics.Errors[0] = "changed"
	script.ProbeResult.Metrics["age"] = 999
	got, _ := channel.Read(context.Background())
	if got.SystemMetrics.Disks[0].Path != `C:\` || got.SystemMetrics.Networks[0].Name != "fake" || got.SystemMetrics.Errors[0] != "old" {
		t.Fatalf("system snapshot mutated: %+v", got)
	}
	got, _ = channel.Read(context.Background())
	if got.ProbeResult.Metrics["age"] != 2 {
		t.Fatal("script metric map mutated after enqueue")
	}
}
