//go:build windows && integration && webui

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestNativeCardActionsBrowser(t *testing.T) {
	f := newAPIFixtureWithHTTPClock(t, time.Date(2026, 11, 9, 6, 0, 0, 0, time.UTC), time.Now)
	runNativeWebBrowser(t, f, "card-actions.spec.ts")
	if f.manager.Snapshot().Notifiers[0].CardActions || len(f.client.sent) != 0 || f.hookFactories != 0 || len(f.toast.values) != 0 {
		t.Fatal("browser left delegation enabled or attempted a notification")
	}
	if _, found := f.vault.values[cardActionKeyName]; found {
		t.Fatal("configuration-only fixture started a listener or created signing credentials")
	}
}

type nativeActionStreamFixture struct {
	handler chan feishu.CardActionHandler
	closed  chan struct{}
}

func (s *nativeActionStreamFixture) Run(ctx context.Context, handler feishu.CardActionHandler, state feishu.ActionConnectionState) error {
	state("connected")
	s.handler <- handler
	<-ctx.Done()
	close(s.closed)
	return nil
}

func TestNativeCardActionsMonitorLifecycleAndCommittedEffect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	home := t.TempDir()
	directory := filepath.Join(home, "config")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"targets.yaml":   "targets: [{id: web, match: {process: fake.exe}}]\n",
		"rules.yaml":     "rules: [{id: down, type: process_absent, target: web, for: 0s, severity: critical, notify: [fake-channel]}]\n",
		"notifiers.yaml": "feishuApps: [{id: fake-app, appId: cli_fake_native_actions, secretRef: fake-ref, domain: feishu}]\nnotifiers: [{id: fake-channel, type: feishu_app, app: fake-app, chatId: oc_fake_native, cardActions: true}]\n",
		"settings.yaml":  "httpListen: 127.0.0.1:0\n",
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	vault, err := secrets.New(home, logger)
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	for name, value := range map[string][]byte{"fake-ref": []byte("fake-native-actions-secret"), cardActionKeyName: key} {
		if err := vault.Set(ctx, name, value); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(home, "data", "lgmon.db")
	db, err := store.Open(ctx, store.Options{Path: path, DisableMaintenance: true})
	if err != nil {
		t.Fatal(err)
	}
	alert := rules.Alert{ID: "fake-native-alert", RuleID: "down", CollectorID: "fake-host", TargetID: "web", State: rules.StateFiring, Severity: "critical", StartedAt: time.Now().Add(-time.Second).UnixMilli(), NotificationKey: "firing:fake-native"}
	if err := db.RegisterCollector(ctx, store.CollectorRecord{ID: "fake-host", Hostname: "虚构主机"}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.SaveTransition(ctx, alert, rules.Event{AlertID: alert.ID, Kind: rules.StateFiring, TS: alert.StartedAt}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.RecordNotification(ctx, notify.Attempt{ID: "fake-native-attempt", AlertID: alert.ID, State: rules.StateFiring, EventKey: alert.NotificationKey, NotifierID: "fake-channel", MessageID: "om_fake_native", OK: true, TS: alert.StartedAt}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	stream := &nativeActionStreamFixture{handler: make(chan feishu.CardActionHandler, 1), closed: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		done <- RunLocal(ctx, LocalOptions{Home: home, Role: "monitor", Listen: "127.0.0.1:0", Logger: logger, NewClient: func(config.FeishuApp, []byte) (feishu.Client, error) { return &apiClient{}, nil }, NewActionStream: func(app config.FeishuApp, secret []byte) (feishu.ActionStream, error) {
			if app.AppID != "cli_fake_native_actions" || string(secret) != "fake-native-actions-secret" {
				return nil, fmt.Errorf("fixture credential mismatch")
			}
			return stream, nil
		}})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(8 * time.Second):
			t.Error("fixture monitor did not stop")
		}
	})
	var handler feishu.CardActionHandler
	select {
	case handler = <-stream.handler:
	case err := <-done:
		t.Fatal("monitor failed before action startup", err)
	case <-time.After(10 * time.Second):
		t.Fatal("monitor did not start action stream")
	}
	cfg, err := config.Load(os.DirFS(directory))
	if err != nil {
		t.Fatal(err)
	}
	value, err := feishu.SignCardAction(key, cfg.FeishuApps[0], cfg.Notifiers[0], alert.ID, alert.NotificationKey, "ack")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(value)
	reply := handler(ctx, feishu.CardAction{AppID: cfg.FeishuApps[0].AppID, EventID: "fake-native-event", OperatorID: "ou_fake_native", ChatID: "oc_fake_native", MessageID: "om_fake_native", Value: encoded})
	if reply.Type != "success" {
		t.Fatal(reply)
	}
	view, err := store.OpenView(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	result, err := view.Query(ctx, `SELECT a.acknowledged_at,e.acknowledged_at FROM alerts a JOIN card_action_events e ON a.id=e.alert_id WHERE a.id='fake-native-alert' AND a.acknowledged_at>0 AND a.acknowledged_at=e.acknowledged_at`)
	if err != nil || len(result.Rows) != 1 {
		t.Fatal("runtime callback did not commit audit", result, err)
	}
	cancel()
	select {
	case <-stream.closed:
	case <-time.After(4 * time.Second):
		t.Fatal("monitor left its action stream running")
	}
}
