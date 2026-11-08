package notify

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	validator "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestEventLogWebhookOptionalFieldsUTCAndUnicodeMatchSchema(t *testing.T) {
	input := cards.Sample("firing")
	for id := range 7 {
		input.Events = append(input.Events, collector.EventLogRecord{TS: 1000 + int64(id), Provider: "Fake", EventID: 1000, Subject: "fake.exe", Message: strings.Repeat("测", 201)})
	}
	payload := WebhookPayload(input, time.UnixMilli(10000), false)
	if len(payload.Data.Events) != 5 || payload.Data.Events[0].Time != "1970-01-01T00:00:01.002Z" || len([]rune(payload.Data.Events[0].Message)) != 200 || payload.Data.Version != 1 {
		t.Fatalf("webhook events: %+v", payload.Data)
	}
	data, err := os.ReadFile("../../schemas/webhook-payload.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	compiler := validator.NewCompiler()
	if err := compiler.AddResource("https://fake.invalid/event-payload", schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("https://fake.invalid/event-payload")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(value); err != nil {
		t.Fatal(err)
	}
	input.Events = nil
	raw, err = json.Marshal(WebhookPayload(input, time.UnixMilli(10000), false))
	if err != nil || strings.Contains(string(raw), `"events"`) {
		t.Fatal("optional events appeared without a diagnostic snapshot")
	}
}
