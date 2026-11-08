package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestCardUpdateUsesOnePatchAndNeverRetriesFailure(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "token rejected"}[failure], func(t *testing.T) {
			var calls atomic.Int32
			card := json.RawMessage(`{"schema":"2.0","config":{"update_multi":true},"body":{"elements":[]}}`)
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
					tokenResponse(w, "fake-tenant", 7200)
					return
				}
				calls.Add(1)
				if r.Method != "PATCH" || r.URL.Path != "/open-apis/im/v1/messages/om_fake" || r.Header.Get("Authorization") != "Bearer fake-tenant" {
					t.Errorf("incorrect update request: %s %s", r.Method, r.URL.Path)
				}
				var body struct {
					Content string `json:"content"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Content != string(card) {
					t.Errorf("incorrect patch card: %s %v", body.Content, err)
				}
				if failure {
					writeJSON(w, map[string]any{"code": 99991663, "msg": "fake expired token"})
				} else {
					writeJSON(w, map[string]any{"code": 0, "data": map[string]any{}})
				}
			}, Options{})
			updater := client.(CardUpdater)
			err := updater.UpdateCard(context.Background(), "om_fake", card)
			if (err != nil) != failure || calls.Load() != 1 {
				t.Fatalf("update retried or wrong result: %v calls=%d", err, calls.Load())
			}
			if err := updater.UpdateCard(context.Background(), "../invalid", card); err == nil || calls.Load() != 1 {
				t.Fatal("invalid path reached API")
			}
		})
	}
}
