package rules

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
)

type fakePresence struct {
	items []CollectorPresence
	err   error
}

func (s *fakePresence) Collectors(context.Context) ([]CollectorPresence, error) {
	return s.items, s.err
}

func offlineFixture(t *testing.T, explicit string) (*fixture, *fakePresence) {
	t.Helper()
	f := newFixture(t, "0s", "process_absent", 0)
	f.cfg.Targets, f.cfg.Rules = nil, nil
	if explicit != "" {
		f.cfg.Rules = []config.Rule{{ID: "offline-policy", Type: "collector_offline", For: config.Duration(explicit), Severity: "critical"}}
	}
	source := &fakePresence{items: []CollectorPresence{{ID: "paired", Hostname: "虚构主机", LastSeen: f.now.UnixMilli()}}}
	opts := f.options()
	opts.Collectors = source
	var err error
	f.engine, err = New(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return f, source
}

func TestOfflineDefaultCountsFromLastContactAndUnknownDoesNotResolve(t *testing.T) {
	f, source := offlineFixture(t, "")
	f.evaluate(t, 29*time.Second)
	noNotification(t, f.notifications)
	f.evaluate(t, time.Second)
	alert := takeAlert(t, f.notifications, StateFiring)
	if alert.RuleID != config.DefaultOfflineRuleID || alert.TargetID != CollectorTargetID || alert.Severity != "warning" || alert.StartedAt != source.items[0].LastSeen || !strings.Contains(alert.Summary, "虚构主机") {
		t.Fatalf("wrong default incident: %+v", alert)
	}
	f.evaluate(t, time.Minute)
	noNotification(t, f.notifications)
	source.items[0].Connected, source.items[0].LastSeen = true, f.now.UnixMilli()
	source.err = errors.New("fake source failure")
	if err := f.engine.Evaluate(context.Background()); !errors.Is(err, source.err) {
		t.Fatalf("source failure hidden: %v", err)
	}
	noNotification(t, f.notifications)
	source.err = nil
	f.evaluate(t, 0)
	resolved := takeAlert(t, f.notifications, StateResolved)
	if resolved.ID != alert.ID || !strings.Contains(resolved.Summary, "已恢复连接") {
		t.Fatalf("wrong recovered incident: %+v", resolved)
	}
}

func TestOfflineHealthyEmptyCollectorAndExplicitShortDelays(t *testing.T) {
	for _, delay := range []string{"0s", "5s", "30s"} {
		t.Run(delay, func(t *testing.T) {
			f, source := offlineFixture(t, delay)
			source.items[0].Connected = true
			for i := 0; i < 6; i++ {
				f.now = f.now.Add(9 * time.Second)
				source.items[0].LastSeen = f.now.UnixMilli()
				f.evaluate(t, 0)
				noNotification(t, f.notifications)
			}
			source.items[0].Connected = false
			f.evaluate(t, 0)
			seconds, _ := config.Duration(delay).Parse()
			f.evaluate(t, seconds)
			alert := takeAlert(t, f.notifications, StateFiring)
			if alert.RuleID != "offline-policy" {
				t.Fatal("automatic policy was duplicated")
			}
			noNotification(t, f.notifications)
		})
	}
}

func TestOfflineOpenStreamMissingHeartbeatsAndRevocation(t *testing.T) {
	f, source := offlineFixture(t, "")
	source.items[0].Connected = true
	f.evaluate(t, 30*time.Second)
	alert := takeAlert(t, f.notifications, StateFiring)
	source.items = nil
	f.evaluate(t, time.Second)
	if got := f.repository.alerts[alert.ID]; got.State != StateResolved || f.repository.events[len(f.repository.events)-1].Kind != "closed" {
		t.Fatalf("revoked identity stayed active: %+v", got)
	}
	noNotification(t, f.notifications)
}

func TestOfflineRestoresFiringIncidentAndRecoversAfterMonitorRestart(t *testing.T) {
	f, source := offlineFixture(t, "")
	f.evaluate(t, 30*time.Second)
	alert := takeAlert(t, f.notifications, StateFiring)
	opts := f.options()
	opts.Collectors = source
	var err error
	f.engine, err = New(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	f.evaluate(t, time.Second)
	noNotification(t, f.notifications)
	source.items[0].Connected, source.items[0].LastSeen = true, f.now.UnixMilli()
	f.evaluate(t, 0)
	if recovered := takeAlert(t, f.notifications, StateResolved); recovered.ID != alert.ID {
		t.Fatal("restart changed incident identity")
	}
}

func TestOfflinePolicyAutomaticallyCoversEveryPairedIdentity(t *testing.T) {
	f, source := offlineFixture(t, "")
	source.items = append(source.items, CollectorPresence{ID: "second-paired", Hostname: "another fake host", LastSeen: f.now.UnixMilli()})
	f.evaluate(t, 30*time.Second)
	first := takeAlert(t, f.notifications, StateFiring)
	second := takeAlert(t, f.notifications, StateFiring)
	if first.CollectorID == second.CollectorID || first.RuleID != config.DefaultOfflineRuleID || second.RuleID != first.RuleID {
		t.Fatalf("global policy did not cover both identities: %+v %+v", first, second)
	}
	noNotification(t, f.notifications)
}

func TestPresenceHeartbeatRetainsIncidentButRenameClosesHostnameSelectedRule(t *testing.T) {
	f := newFixture(t, "0s", "process_absent", 0)
	f.cfg.Targets[0].Collector = "assigned-fake-host"
	source := &fakePresence{items: []CollectorPresence{{ID: "host-test", Hostname: "assigned-fake-host", LastSeen: f.now.UnixMilli(), Connected: true}}}
	opts := f.options()
	opts.Collectors = source
	opts.CollectorHostname = func(context.Context, string) (string, error) { return source.items[0].Hostname, nil }
	var err error
	f.engine, err = New(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, collector.StateDown)
	alert := takeAlert(t, f.notifications, StateFiring)
	for index := 0; index < 3; index++ {
		f.now = f.now.Add(time.Second)
		source.items[0].LastSeen = f.now.UnixMilli()
		source.items[0].Connected = !source.items[0].Connected
		f.evaluate(t, 0)
		if got := f.repository.alerts[alert.ID]; got.State != StateFiring {
			t.Fatalf("heartbeat changed the target incident: %+v", got)
		}
		noNotification(t, f.notifications)
	}
	source.items[0].Hostname = "another-fake-host"
	f.evaluate(t, 0)
	if got := f.repository.alerts[alert.ID]; got.State != StateResolved || f.repository.events[len(f.repository.events)-1].Kind != "closed" {
		t.Fatalf("renamed collector kept the old assignment: %+v", got)
	}
	noNotification(t, f.notifications)
}

func TestPresenceIdentityReplacementClosesRevokedOfflineIncident(t *testing.T) {
	f, source := offlineFixture(t, "")
	f.evaluate(t, 30*time.Second)
	alert := takeAlert(t, f.notifications, StateFiring)
	source.items = []CollectorPresence{{ID: "new-fake-pair", Hostname: "new-fake-host", LastSeen: f.now.UnixMilli(), Connected: true}}
	f.evaluate(t, 0)
	if got := f.repository.alerts[alert.ID]; got.State != StateResolved || f.repository.events[len(f.repository.events)-1].Kind != "closed" {
		t.Fatalf("same-size identity replacement retained the revoked incident: %+v", got)
	}
	noNotification(t, f.notifications)
}
