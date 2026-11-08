package transport

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/protobuf/proto"
)

func TestHTTPCheckCapabilityBlocksOnlyAssignedUnsupportedConfiguration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	absent := false
	cfg := &config.Config{Targets: []config.Target{
		{ID: "keyword", Collector: "legacy", Match: config.Match{Process: "fake.exe"}, Probes: []config.Probe{{Type: "http", URL: "http://fake.invalid/", Interval: "30s", Timeout: "2s", Keyword: "healthy", KeywordAbsent: &absent}}},
		{ID: "ordinary", Collector: "other", Match: config.Match{Process: "fake.exe"}},
	}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	manager, err := config.NewManager(initialTargets{cfg}, logger)
	if err != nil {
		t.Fatal(err)
	}
	receiver := NewReceiver(nil)
	clock := configStateClock{created: make(chan *configStateTicker, 1)}
	receiver.SetConfigDelivery(ConfigDelivery{Source: manager, Clock: clock, Logger: logger})
	session := &linkSession{identity: Hello{CollectorID: "legacy", Capabilities: []string{MatchDirectoryCapability}}}
	receiver.active["legacy"] = session
	var sends atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- receiver.pushConfig(ctx, session, func(*lgmonv1.MonitorMessage) error { sends.Add(1); return nil }, make(chan *lgmonv1.ConfigApplied))
	}()
	ticker := <-clock.created
	state := waitConfigState(t, ctx, receiver, "legacy", "outdated")
	if sends.Load() != 0 || !strings.Contains(state.Message, "keyword") || !strings.Contains(state.Message, HTTPChecksCapability) || !strings.Contains(state.Message, "keyword") {
		t.Fatalf("unsupported keyword was sent or not explained: %+v", state)
	}
	ticker.ticks <- time.Time{}
	cancel()
	if err := <-done; err != nil || sends.Load() != 0 {
		t.Fatalf("unsupported configuration retried: %d %v", sends.Load(), err)
	}
	if reason := missingConfigCapability(cfg, Hello{CollectorID: "other"}); reason != "" {
		t.Fatal(reason)
	}
	if reason := missingConfigCapability(cfg, Hello{CollectorID: "legacy", Capabilities: []string{HTTPChecksCapability}}); reason != "" {
		t.Fatal(reason)
	}
	_, data, err := config.TargetConfig(cfg, "legacy", "")
	if err != nil || !strings.Contains(string(data), `"keywordAbsent":false`) {
		t.Fatalf("explicit false lost from config: %s %v", data, err)
	}
}

func TestCertificateMetadataSurvivesProtobufRoundTripAndLegacyDefaults(t *testing.T) {
	for _, expires := range []int64{0, 2000000000000} {
		subject := ""
		if expires != 0 {
			subject = "fake certificate"
		}
		message := pipeline.Message{CollectorID: "fake", Seq: 1, TS: 1000, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "web", Type: "http", TLSNotAfter: expires, TLSSubject: subject}}
		wire, err := Encode(message)
		if err != nil {
			t.Fatal(err)
		}
		data, err := proto.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		var decoded lgmonv1.Record
		if err := proto.Unmarshal(data, &decoded); err != nil {
			t.Fatal(err)
		}
		got, err := Decode("fake", &decoded)
		if err != nil || got.ProbeResult.TLSNotAfter != expires || got.ProbeResult.TLSSubject != subject {
			t.Fatalf("TLS metadata changed: %+v %v", got, err)
		}
	}
}
