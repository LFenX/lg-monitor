package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPersonalCardsUseOfficialOpenIDAndKeepChatUUIDCompatible(t *testing.T) {
	type received struct{ Kind, ID, UUID string }
	var receivedMessages []received
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			tokenResponse(w, "fake-token-not-real", 7200)
			return
		}
		var body struct {
			ReceiveID string `json:"receive_id"`
			UUID      string `json:"uuid"`
			Content   string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !json.Valid([]byte(body.Content)) {
			t.Error("invalid official SDK card payload", err)
		}
		receivedMessages = append(receivedMessages, received{r.URL.Query().Get("receive_id_type"), body.ReceiveID, body.UUID})
		writeJSON(w, map[string]any{"code": 0, "data": map[string]string{"message_id": "om_fake_private"}})
	}, Options{})
	message := CardMessage{AlertID: "fake-private-alert", State: "firing", OpenID: "ou_fake_person", Card: json.RawMessage(`{"schema":"2.0","body":{"elements":[]}}`)}
	for range 2 {
		if _, err := client.SendCard(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	message.State = "resolved"
	if _, err := client.SendCard(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	message.OpenID, message.ChatID, message.State = "", "ou_fake_person", "firing"
	if _, err := client.SendCard(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if len(receivedMessages) != 4 || receivedMessages[0].Kind != "open_id" || receivedMessages[0].ID != "ou_fake_person" || receivedMessages[0].UUID != receivedMessages[1].UUID || receivedMessages[0].UUID == receivedMessages[2].UUID || receivedMessages[0].UUID == receivedMessages[3].UUID || receivedMessages[3].Kind != "chat_id" || receivedMessages[3].UUID != MessageUUID(message.AlertID, "firing", message.ChatID) {
		t.Fatal("destination namespace or idempotence changed", receivedMessages)
	}
	message.OpenID = "ou_fake_person"
	if _, err := client.SendCard(context.Background(), message); err == nil || len(receivedMessages) != 4 {
		t.Fatal("ambiguous destination sent")
	}
	message.OpenID, message.ChatID = "", ""
	if _, err := client.SendCard(context.Background(), message); err == nil || len(receivedMessages) != 4 {
		t.Fatal("missing destination sent")
	}
}
