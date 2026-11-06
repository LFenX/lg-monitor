package feishu

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestChatDetailsUserCountIsOptional(t *testing.T) {
	for _, count := range []string{"0", "7", "missing", "invalid"} {
		t.Run(count, func(t *testing.T) {
			client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "tenant_access_token") {
					tokenResponse(w, "fake-token", 7200)
					return
				}
				if r.URL.Path != "/open-apis/im/v1/chats/oc_fake_group" || r.URL.Query().Get("user_id_type") != "open_id" {
					t.Error("incorrect SDK chat details request")
				}
				data := map[string]any{"name": "虚构群"}
				if count != "missing" {
					data["user_count"] = count
				}
				writeJSON(w, map[string]any{"code": 0, "data": data})
			}, Options{})
			chat, err := client.ChatInfo(context.Background(), "oc_fake_group")
			if count == "invalid" {
				if err == nil {
					t.Fatal("invalid user count was accepted")
				}
				return
			}
			if err != nil || chat.Name != "虚构群" || chat.ID != "oc_fake_group" {
				t.Fatalf("chat details: %+v %v", chat, err)
			}
			if count == "missing" && chat.MemberCount != nil {
				t.Fatal("unknown user count was replaced with zero")
			}
			if count != "missing" && chat.MemberCount == nil {
				t.Fatal("known user count was discarded")
			}
		})
	}
}
