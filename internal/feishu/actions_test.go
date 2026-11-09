package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

func actionSDKPayload() map[string]any {
	return map[string]any{
		"schema": "2.0", "header": map[string]any{"event_id": "fake-event", "event_type": "card.action.trigger", "app_id": "cli_fake_app"},
		"event": map[string]any{"operator": map[string]any{"open_id": "ou_fake_operator"}, "token": "fake-update-token-never-forward", "action": map[string]any{"tag": "button", "value": map[string]any{"operation": "ack"}}, "context": map[string]any{"open_chat_id": "oc_fake_group", "open_message_id": "om_fake_card"}},
	}
}

func TestActionSDKDispatcherNormalizesNativeFieldsAndRejectsForeignOrMalformedCallbacks(t *testing.T) {
	var logs bytes.Buffer
	calls := 0
	d := actionDispatcher("cli_fake_app", func(ctx context.Context, action CardAction) CardActionReply {
		calls++
		if action.OperatorID != "ou_fake_operator" || action.ChatID != "oc_fake_group" || action.MessageID != "om_fake_card" || action.EventID != "fake-event" {
			t.Fatal("SDK field mismatch", action)
		}
		if strings.Contains(string(action.Value), "fake-update-token") {
			t.Fatal("platform update token escaped normalization")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Second {
			t.Fatal("callback lacks bounded deadline")
		}
		return CardActionReply{Type: "success", Message: "已确认"}
	}, slog.New(slog.NewTextHandler(&logs, nil)))
	data, _ := json.Marshal(actionSDKPayload())
	value, err := d.Do(context.Background(), data)
	if err != nil || calls != 1 {
		t.Fatal(value, err)
	}
	response, ok := value.(*callback.CardActionTriggerResponse)
	if !ok || response.Toast == nil || response.Toast.Content != "已确认" {
		t.Fatal("wrong SDK toast", value)
	}
	for _, failure := range []string{"app", "operator", "message", "chat", "tag", "large"} {
		payload := actionSDKPayload()
		event := payload["event"].(map[string]any)
		switch failure {
		case "app":
			payload["header"].(map[string]any)["app_id"] = "cli_wrong_app"
		case "operator":
			delete(event["operator"].(map[string]any), "open_id")
		case "message":
			delete(event["context"].(map[string]any), "open_message_id")
		case "chat":
			delete(event["context"].(map[string]any), "open_chat_id")
		case "tag":
			event["action"].(map[string]any)["tag"] = "select_static"
		case "large":
			event["action"].(map[string]any)["value"] = map[string]any{"padding": strings.Repeat("x", 17000)}
		}
		data, _ := json.Marshal(payload)
		value, err := d.Do(context.Background(), data)
		if err != nil || calls != 1 {
			t.Fatal("invalid callback reached business handler", failure, calls, err)
		}
		if response := value.(*callback.CardActionTriggerResponse); response.Toast == nil || response.Toast.Type != "error" {
			t.Fatal("invalid callback reported success", failure)
		}
	}
	if strings.Contains(logs.String(), "fake-update-token") || strings.Contains(logs.String(), "ou_fake_operator") {
		t.Fatal("SDK leaked callback payload")
	}
}

type fakeActionSocket struct {
	connected chan struct{}
	closed    atomic.Bool
	state     ActionConnectionState
}

func (s *fakeActionSocket) Start(ctx context.Context) error {
	s.state("connected")
	close(s.connected)
	<-ctx.Done()
	return ctx.Err()
}
func (s *fakeActionSocket) CloseAndWait(context.Context) error { s.closed.Store(true); return nil }

func TestActionStreamCancellationJoinsSocketAndClearsOwnedCredential(t *testing.T) {
	original := []byte("fake-action-stream-secret")
	stream, err := NewActionStream(config.FeishuApp{AppID: "cli_fake_app", Domain: "feishu", Proxy: "none"}, original, ActionOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	native := stream.(*nativeActionStream)
	socket := &fakeActionSocket{connected: make(chan struct{})}
	native.newSocket = func(_ *dispatcher.EventDispatcher, state ActionConnectionState) actionSocket {
		socket.state = state
		return socket
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- stream.Run(ctx, func(context.Context, CardAction) CardActionReply { return CardActionReply{} }, nil)
	}()
	select {
	case <-socket.connected:
	case <-time.After(time.Second):
		t.Fatal("action stream did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("action stream failed to join")
	}
	if !socket.closed.Load() || !bytes.Equal(native.secret, make([]byte, len(original))) || string(original) != "fake-action-stream-secret" {
		t.Fatal("owned credential or connection leaked")
	}
}
