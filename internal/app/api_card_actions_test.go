package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/config"
)

func TestCardActionAPIRequiresDedicatedAdminDelegation(t *testing.T) {
	f := newAPIFixture(t)
	path := "/api/v1/notifiers/fake-channel/card-actions"
	f.request(t, "GET", path, "read", "", 200)
	for _, role := range []string{"read", "config"} {
		f.request(t, "PUT", path, role, `{"enabled":true}`, 403)
	}
	body := config.NotifiersFile{FeishuApps: f.manager.Snapshot().FeishuApps, Notifiers: f.manager.Snapshot().Notifiers}
	body.Notifiers[0].CardActions = true
	data, _ := json.Marshal(body)
	for _, role := range []string{"config", "admin"} {
		f.request(t, "PUT", "/api/v1/config/notifiers", role, string(data), 403)
	}
	f.request(t, "PUT", path, "admin", `{"enabled":true}`, 200)
	if !f.manager.Snapshot().Notifiers[0].CardActions {
		t.Fatal("dedicated grant not saved")
	}
	if !strings.Contains(string(f.files.MapFS["notifiers.yaml"].Data), "# preserve this comment") {
		t.Fatal("grant lost comments")
	}
	f.request(t, "PUT", "/api/v1/config/notifiers", "config", string(data), 200)
	body.Notifiers[0].ChatID = "oc_other_destination"
	data, _ = json.Marshal(body)
	f.request(t, "PUT", "/api/v1/config/notifiers", "admin", string(data), 403)
	body.Notifiers[0].CardActions = false
	data, _ = json.Marshal(body)
	f.request(t, "PUT", "/api/v1/config/notifiers", "config", string(data), 200)
	f.request(t, "GET", "/api/v1/notifiers/unknown/card-actions", "read", "", 404)
	f.request(t, "PUT", path, "admin", `{"enabled":true,"operator":"any"}`, 400)
}
