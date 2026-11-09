package transport

import (
	"testing"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestSamplingPeriodWireCompatibility(t *testing.T) {
	for _, period := range []int64{0, 500, 4000, 5000} {
		message := pipeline.Message{CollectorID: "fake-collector", Seq: 1, TS: 100000, Kind: pipeline.KindProcessSample, ProcessSample: &pipeline.ProcessSample{TargetID: "web", PID: 42, CreatedAt: 1, CPUPercent: 2, RSS: 4096, IntervalMillis: period}}
		record, err := Encode(message)
		if err != nil {
			t.Fatal(err)
		}
		restored, err := Decode("fake-collector", record)
		if err != nil || restored.ProcessSample.IntervalMillis != period {
			t.Fatal(restored, err)
		}
	}
}
