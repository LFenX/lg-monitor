package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestMessageUUIDSeparatesChatsAndRemainsStableForRetries(t *testing.T) {
	first := MessageUUID("fake-alert", "firing", "oc_fake_one")
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(first) || first != MessageUUID("fake-alert", "firing", "oc_fake_one") {
		t.Fatal("retry UUID must be stable and forty hex characters")
	}
	if first == MessageUUID("fake-alert", "firing", "oc_fake_two") || first == MessageUUID("fake-alert", "resolved", "oc_fake_one") {
		t.Fatal("UUID must distinguish destination chat and transition state")
	}
	var uuids, receivers []string
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			tokenResponse(w, "fake-token", 7200)
			return
		}
		var body struct {
			ReceiveID string `json:"receive_id"`
			UUID      string `json:"uuid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		uuids, receivers = append(uuids, body.UUID), append(receivers, body.ReceiveID)
		writeJSON(w, map[string]any{"code": 0, "data": map[string]string{"message_id": "om_fake_uuid"}})
	}, Options{})
	for _, chat := range []string{"oc_fake_one", "oc_fake_two", "oc_fake_one"} {
		if _, err := client.SendCard(context.Background(), CardMessage{AlertID: "fake-alert", State: "firing", ChatID: chat, Card: json.RawMessage(`{"schema":"2.0"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(uuids) != 3 || uuids[0] == uuids[1] || uuids[0] != uuids[2] || uuids[0] != first || receivers[1] != "oc_fake_two" {
		t.Fatalf("SDK destination/dedup mismatch: %v %v", receivers, uuids)
	}
}
