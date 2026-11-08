package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func storedCertificateMessage(at, expires int64, subject string) pipeline.Message {
	return pipeline.Message{CollectorID: "fake", Seq: 1, TS: at, Kind: pipeline.KindProbeResult,
		ProbeResult: &pipeline.ProbeResult{TargetID: "web", Type: "http", TS: at, OK: expires != 0, TLSNotAfter: expires, TLSSubject: subject}}
}

func TestCertificateRawStorageLatestViewAndHistory(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(2000000000000)
	s := openStore(t, Options{Now: func() time.Time { return now }, DisableMaintenance: true})
	expiry := now.Add(3 * 24 * time.Hour).UnixMilli()
	for _, message := range []pipeline.Message{
		storedCertificateMessage(now.UnixMilli(), expiry, "old"),
		storedCertificateMessage(now.Add(time.Second).UnixMilli(), expiry+1000, "first same timestamp"),
		storedCertificateMessage(now.Add(time.Second).UnixMilli(), expiry+2000, "latest same timestamp"),
		storedCertificateMessage(now.Add(2*time.Second).UnixMilli(), 0, ""),
	} {
		if err := s.Write(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var unavailable sql.NullInt64
	if err := s.db.QueryRow(`SELECT tls_not_after FROM probe_results ORDER BY ts DESC LIMIT 1`).Scan(&unavailable); err != nil || unavailable.Valid {
		t.Fatalf("missing certificate stored as a timestamp: %+v %v", unavailable, err)
	}
	view, err := OpenViewWithClock(ctx, sPath(t, s), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	for _, step := range []string{"raw", "1m", "1h"} {
		history, err := view.TargetHistory(ctx, "fake", "web", HistoryRange{From: now.Add(-time.Hour).UnixMilli(), To: now.Add(time.Minute).UnixMilli(), Step: step})
		if err != nil || len(history.Certificates) != 1 || history.Certificates[0].TLSNotAfter != expiry+2000 || history.Certificates[0].TLSSubject != "latest same timestamp" {
			t.Fatalf("latest certificate lost at %s: %+v %v", step, history, err)
		}
		if step == "raw" && (len(history.Probes) != 4 || history.Probes[0].TLSNotAfter == nil || *history.Probes[0].TLSNotAfter != expiry || history.Probes[3].TLSNotAfter != nil) {
			t.Fatalf("raw TLS metadata changed: %+v", history.Probes)
		}
	}
}

func TestCertificateNotificationSnapshotsSurviveRenewalAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "monitor.db")
	s := openStore(t, Options{Path: path, DisableMaintenance: true})
	alert := testAlert()
	alert.Cert = &rules.Certificate{NotAfter: 2000000000000, Subject: "old certificate"}
	if err := s.SaveTransition(ctx, alert, testEvent(alert)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openStore(t, Options{Path: path, DisableMaintenance: true})
	active, err := s.ActiveAlerts(ctx)
	if err != nil || len(active) != 1 || !reflect.DeepEqual(active[0].Cert, alert.Cert) {
		t.Fatalf("active certificate lost on restart: %+v %v", active, err)
	}
	old := *alert.Cert
	alert.Cert = &rules.Certificate{NotAfter: 2000010000000, Subject: "renewed certificate"}
	alert.State, alert.ResolvedAt, alert.NotificationAt, alert.NotificationKey = rules.StateResolved, 1200, 1200, "resolved:1200"
	if err := s.SaveTransition(ctx, alert, testEvent(alert)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openStore(t, Options{Path: path, DisableMaintenance: true})
	pending, err := s.PendingNotifications(ctx, 1000, 2000, 0, 10)
	if err != nil || len(pending) != 2 || pending[0].Alert.Cert == nil || *pending[0].Alert.Cert != old || !reflect.DeepEqual(pending[1].Alert.Cert, alert.Cert) {
		t.Fatalf("renewal overwrote a delayed event's certificate: %+v %v", pending, err)
	}
}

func certificateReplayConfig() *config.Config {
	return &config.Config{Targets: []config.Target{{ID: "web", Match: config.Match{Process: "fake.exe"}, Probes: []config.Probe{
		{Type: "http", URL: "https://fake.invalid/", Interval: "1s", Timeout: "1s"},
	}}}, Rules: []config.Rule{{ID: "cert", Type: "cert_expiry", Target: "web", Severity: "warning", Renotify: "1h"}}}
}

func TestStoredCertificateReplayMatchesRealTimeWithoutWrites(t *testing.T) {
	ctx := context.Background()
	start := int64(2000000000000)
	now := time.UnixMilli(start)
	s := openStore(t, Options{Now: func() time.Time { return now }, DisableMaintenance: true})
	cfg := certificateReplayConfig()
	capture := &replayCapture{}
	engine, err := rules.New(ctx, rules.Options{Config: cfg, Repository: capture, Notifications: capture, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []pipeline.Message{
		storedCertificateMessage(start, start+(3*24*time.Hour).Milliseconds(), "soon"),
		storedCertificateMessage(start+1000, 0, ""),
		storedCertificateMessage(start+2000, start+(90*24*time.Hour).Milliseconds(), "renewed"),
		storedCertificateMessage(start+3000, start-time.Hour.Milliseconds(), "expired"),
	} {
		now = time.UnixMilli(message.TS)
		if err := engine.Handle(ctx, message); err != nil {
			t.Fatal(err)
		}
		if err := s.Write(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := OpenViewWithClock(ctx, sPath(t, s), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	before := count(t, s, "alerts") + count(t, s, "alert_events") + count(t, s, "notifications")
	result, err := rules.Replay(ctx, rules.ReplayOptions{Config: cfg, Rule: cfg.Rules[0], Since: time.UnixMilli(start), Until: time.UnixMilli(start + 4000), Source: view})
	if err != nil || !result.Complete || len(result.Events) != 3 || !reflect.DeepEqual(result.Events, capture.events) {
		t.Fatalf("certificate replay differs: replay=%+v live=%+v err=%v", result.Events, capture.events, err)
	}
	if count(t, s, "alerts")+count(t, s, "alert_events")+count(t, s, "notifications") != before {
		t.Fatal("replay wrote real state")
	}
}

type certificateNotifications struct{ alerts []rules.Alert }

func (c *certificateNotifications) Enqueue(_ context.Context, alert rules.Alert) error {
	c.alerts = append(c.alerts, alert)
	return nil
}

func TestCertificateRulesUseDurableSilenceAcknowledgementAndRecovery(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(2000000000000)
	s := openStore(t, Options{Now: func() time.Time { return now }, DisableMaintenance: true})
	cfg := certificateReplayConfig()
	queue := &certificateNotifications{}
	engine, err := rules.New(ctx, rules.Options{Config: cfg, Repository: s, Notifications: queue, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddSilence(ctx, "cert", "", time.Minute, "fake maintenance"); err != nil {
		t.Fatal(err)
	}
	if err := engine.Handle(ctx, storedCertificateMessage(now.UnixMilli(), now.Add(3*24*time.Hour).UnixMilli(), "soon")); err != nil {
		t.Fatal(err)
	}
	if len(queue.alerts) != 0 {
		t.Fatal("silenced certificate fired a notification")
	}
	now = now.Add(time.Hour)
	if err := engine.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if len(queue.alerts) != 1 || queue.alerts[0].Cert == nil {
		t.Fatalf("certificate reminder missing: %+v", queue.alerts)
	}
	if err := s.AcknowledgeAlert(ctx, queue.alerts[0].ID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if err := engine.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if len(queue.alerts) != 1 {
		t.Fatal("acknowledged certificate kept reminding")
	}
	if err := engine.Handle(ctx, storedCertificateMessage(now.UnixMilli(), now.Add(90*24*time.Hour).UnixMilli(), "renewed")); err != nil {
		t.Fatal(err)
	}
	if len(queue.alerts) != 2 || queue.alerts[1].State != rules.StateResolved || queue.alerts[1].Cert.Subject != "renewed" {
		t.Fatalf("acknowledgement swallowed recovery: %+v", queue.alerts)
	}
}
