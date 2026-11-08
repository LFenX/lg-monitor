package transport

import (
	"reflect"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
)

func TestEventLogRecordRoundTripAndInvalidPayloads(t *testing.T) {
	for _, message := range []pipeline.Message{
		{CollectorID: "fake", Seq: 1, TS: 1000, Kind: pipeline.KindEventLog, EventLog: &collector.EventLogRecord{Channel: "Application", Provider: "Application Error", EventID: 1000, Level: 2, RecordID: 9007199254740993, TS: 1000, Subject: "fake.exe", Message: "测试", Presets: []string{"app_crash"}, Targets: []collector.EventLogTarget{{ID: "fake", Via: "both"}}}},
		{CollectorID: "fake", Seq: 2, TS: 1001, Kind: pipeline.KindEventLogNotice, EventLogNotice: &collector.EventLogNotice{Kind: "eventlog_dropped", Channel: "System", Message: "fake rate limit", Count: 42}},
	} {
		wire, err := Encode(message)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := Decode(message.CollectorID, wire)
		if err != nil || !reflect.DeepEqual(message, decoded) {
			t.Fatalf("round trip differs: %+v %v", decoded, err)
		}
	}
	for _, body := range []*lgmonv1.Record_EventLog{
		{},
		{EventLog: &lgmonv1.EventLogEvent{Channel: "Security", Provider: "fake", EventId: 1, RecordId: 1, Targets: []*lgmonv1.EventLogTarget{{Id: "fake", Via: "subscription"}}}},
		{EventLog: &lgmonv1.EventLogEvent{Channel: "Application", Provider: "fake", EventId: 1, RecordId: 1, Level: 256, Targets: []*lgmonv1.EventLogTarget{{Id: "fake", Via: "subscription"}}}},
		{EventLog: &lgmonv1.EventLogEvent{Channel: "Application", Provider: "fake", EventId: 1, RecordId: 1, Targets: []*lgmonv1.EventLogTarget{nil}}},
	} {
		if _, err := Decode("fake", &lgmonv1.Record{Seq: 1, Ts: 1000, Body: body}); err == nil {
			t.Fatal("invalid event log payload accepted")
		}
	}
}
