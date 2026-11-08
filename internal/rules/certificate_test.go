package rules

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func certificateFixture(t *testing.T) (*Engine, *memoryRepository, *notificationQueue, *time.Time, *config.Config) {
	t.Helper()
	target := config.Target{ID: "blog", Match: config.Match{Process: "node.exe"}, Probes: []config.Probe{
		{Type: "http", URL: "https://fake.invalid/", Interval: "30s", Timeout: "2s"},
		{Type: "http", URL: "https://second.invalid/", Interval: "30s", Timeout: "2s"},
		{Type: "http", URL: "http://plain.invalid/", Interval: "30s", Timeout: "2s"},
	}}
	rule := config.Rule{ID: "cert", Type: "cert_expiry", Target: "blog", Severity: "warning", Renotify: "1h"}
	engine, repository, queue, now := newAdditionalEngine(t, rule, &target)
	*now = time.Date(2030, 1, 2, 3, 4, 5, 0, time.FixedZone("fake-local", 8*3600))
	return engine, repository, queue, now, &config.Config{Targets: []config.Target{target}, Rules: []config.Rule{rule}}
}

func certificateMessage(host string, index int, at time.Time, expires int64) pipeline.Message {
	return pipeline.Message{CollectorID: host, Seq: 1, TS: at.UnixMilli(), Kind: pipeline.KindProbeResult,
		ProbeResult: &pipeline.ProbeResult{TargetID: "blog", Probe: index, TS: at.UnixMilli(), Type: "http", OK: expires > 0, TLSNotAfter: expires, TLSSubject: func() string {
			if expires > 0 {
				return "fake certificate"
			}
			return ""
		}()}}
}

func TestCertificateInstancesBoundaryRenewalAndUnavailableObservations(t *testing.T) {
	engine, repo, queue, now, _ := certificateFixture(t)
	send := func(host string, index int, expires int64) {
		t.Helper()
		if err := engine.Handle(context.Background(), certificateMessage(host, index, *now, expires)); err != nil {
			t.Fatal(err)
		}
	}
	limit := now.Add(14 * 24 * time.Hour)
	send("fake-a", 0, limit.UnixMilli())
	first := takeAlert(t, queue, StateFiring)
	if first.Cert == nil || first.Cert.NotAfter != limit.UnixMilli() ||
		first.InstanceKey != CertificateInstanceKey("fake-a", "blog", 0) || !strings.Contains(first.Summary, limit.In(now.Location()).Format("2006-01-02")) {
		t.Fatalf("certificate boundary or local date changed: %+v", first)
	}
	send("fake-a", 1, now.Add(-24*time.Hour).UnixMilli())
	expired := takeAlert(t, queue, StateFiring)
	send("fake-b", 0, now.Add(3*24*time.Hour).UnixMilli())
	other := takeAlert(t, queue, StateFiring)
	if expired.ID == first.ID || other.ID == first.ID || expired.InstanceKey == other.InstanceKey || !strings.Contains(expired.Summary, "证书已于") {
		t.Fatalf("certificate instances collided: %+v %+v", expired, other)
	}
	send("fake-a", 2, now.Add(-24*time.Hour).UnixMilli())
	noNotification(t, queue)
	send("fake-a", 0, 0)
	*now = now.Add(2 * time.Minute)
	if err := engine.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	noNotification(t, queue)
	if len(repo.events) != 3 {
		t.Fatal("missing certificate changed firing state")
	}
	send("fake-a", 0, now.Add(14*24*time.Hour+time.Millisecond).UnixMilli())
	resolved := takeAlert(t, queue, StateResolved)
	if resolved.ID != first.ID || resolved.Cert.NotAfter <= limit.UnixMilli() || !strings.Contains(resolved.Summary, "证书已续期") {
		t.Fatalf("renewal did not resolve original instance: %+v", resolved)
	}
	send("unavailable", 0, now.Add(14*24*time.Hour+time.Hour).UnixMilli())
	send("unavailable", 0, 0)
	*now = now.Add(2 * time.Hour)
	if err := engine.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Other still-known instances may remind, but a failed observation cannot start a new incident.
	for len(queue.alerts) > 0 {
		if alert := <-queue.alerts; alert.CollectorID == "unavailable" {
			t.Fatalf("unavailable site newly fired: %+v", alert)
		}
	}
}

func TestCertificateRenotifyAndRuleChangesReuseLifecycle(t *testing.T) {
	engine, repo, queue, now, cfg := certificateFixture(t)
	if err := engine.Handle(context.Background(), certificateMessage("fake-a", 0, *now, now.Add(3*24*time.Hour).UnixMilli())); err != nil {
		t.Fatal(err)
	}
	first := takeAlert(t, queue, StateFiring)
	*now = now.Add(25 * time.Hour)
	if err := engine.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	reminder := takeAlert(t, queue, StateFiring)
	if reminder.ID != first.ID || !strings.HasPrefix(reminder.NotificationKey, "renotify:") || reminder.Cert == nil || !strings.Contains(reminder.Summary, "2 天后到期") {
		t.Fatalf("certificate reminder identity, snapshot, or countdown is wrong: %+v", reminder)
	}
	cfg.Rules[0].Within = "1d"
	if err := engine.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	noNotification(t, queue)
	if repo.events[len(repo.events)-1].Kind != "closed" {
		t.Fatal("threshold change did not close old condition")
	}
}

func TestCertificateBackfillUsesSourceTimePerProbeAndSuppressesFinishedIncident(t *testing.T) {
	engine, repo, queue, now, _ := certificateFixture(t)
	start := now.Add(-10 * time.Minute)
	send := func(index int, at time.Time, expires int64) {
		t.Helper()
		if err := engine.Handle(context.Background(), certificateMessage("fake-a", index, at, expires)); err != nil {
			t.Fatal(err)
		}
	}
	send(0, start, now.Add(time.Hour).UnixMilli())
	send(1, start.Add(time.Minute), now.Add(time.Hour).UnixMilli())
	noNotification(t, queue)
	send(0, now.Add(-time.Minute), now.Add(90*24*time.Hour).UnixMilli())
	send(1, now.Add(-time.Second), now.Add(time.Hour).UnixMilli())
	alert := takeAlert(t, queue, StateFiring)
	if alert.InstanceKey != CertificateInstanceKey("fake-a", "blog", 1) || alert.StartedAt != start.Add(time.Minute).UnixMilli() || len(repo.events) != 1 {
		t.Fatalf("backfill mixed instances or source times: %+v %+v", alert, repo.events)
	}
	noNotification(t, queue)
}
