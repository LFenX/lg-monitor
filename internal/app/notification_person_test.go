package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

func TestPersonalNotifierHTTPMCPShareDestinationLimitAndReadScope(t *testing.T) {
	f := newAPIFixture(t)
	cfg := f.manager.Snapshot()
	cfg.Notifiers[0].ChatID, cfg.Notifiers[0].OpenID = "", "ou_fake_person"
	alias := cfg.Notifiers[0]
	alias.ID = "fake-person-alias"
	cfg.Notifiers = append(cfg.Notifiers, alias)
	data, _ := json.Marshal(config.NotifiersFile{FeishuApps: cfg.FeishuApps, Notifiers: cfg.Notifiers})
	f.files.MapFS["notifiers.yaml"] = &fstest.MapFile{Data: data}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", "/api/v1/notifiers/fake-channel/test", "read", `{}`, 403)
	f.request(t, "POST", "/api/v1/notifiers/fake-channel/test", "config", `{}`, 200)
	if len(f.client.sent) != 1 || f.client.sent[0].OpenID != "ou_fake_person" || f.client.sent[0].ChatID != "" {
		t.Fatal("private test used group destination")
	}
	f.request(t, "POST", "/api/v1/notifiers/fake-person-alias/test", "config", `{}`, 429)
	httpServer := httptest.NewServer(f.server)
	t.Cleanup(httpServer.Close)
	session := mcpSession(t, httpServer.URL, "fake-config", false)
	if !strings.Contains(mcpCall(t, session, "test_notifier", map[string]any{"id": alias.ID}, true), "notification_rate_limited") {
		t.Fatal("MCP bypassed private destination limit")
	}
	f.now = f.now.Add(11 * time.Second)
	mcpCall(t, session, "test_notifier", map[string]any{"id": alias.ID}, false)
	if len(f.client.sent) != 2 || f.client.sent[1].OpenID != "ou_fake_person" {
		t.Fatal("MCP did not retain open_id")
	}
}

func TestPersonalRuntimeRoutesAndSuppressesAReassignedRecipient(t *testing.T) {
	for _, reassign := range []bool{false, true} {
		t.Run(map[bool]string{false: "send", true: "reassigned"}[reassign], func(t *testing.T) {
			f := newRuntimeFixture(t)
			close(f.client.release)
			alert := seedUnsent(t, f, false)
			q := policyQueue(t, f)
			q.active.Notifiers[0].ChatID, q.active.Notifiers[0].OpenID = "", "ou_fake_person"
			q.active.Notifiers[0].Filter = &config.NotifierFilter{Severity: []string{alert.Severity}, Collectors: []string{alert.CollectorID}, Tags: []string{"web"}}
			q.active.Targets[0].Tags = []string{"web"}
			if err := q.Enqueue(context.Background(), alert); err != nil {
				t.Fatal(err)
			}
			if reassign {
				updated := *q.active
				updated.Notifiers = append([]config.Notifier{}, q.active.Notifiers...)
				updated.Notifiers[0].OpenID = "ou_fake_reassigned"
				q.setConfig(&updated)
			}
			close(q.jobs)
			if err := q.run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if reassign {
				if len(f.client.wire) != 0 || len(f.db.attempts) != 0 {
					t.Fatal("reassigned recipient received queued notification")
				}
				return
			}
			if len(f.client.wire) != 1 || len(f.db.attempts) != 1 {
				t.Fatal("private notification missing")
			}
			message := <-f.client.wire
			if message.OpenID != "ou_fake_person" || message.ChatID != "" || message.AlertID != alert.ID {
				t.Fatal("wrong private recipient")
			}
		})
	}
}
