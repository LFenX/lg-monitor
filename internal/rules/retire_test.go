package rules

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
)

func TestDeletedOrChangedRulesCloseWithoutRecoveryNotification(t *testing.T) {
	for _, change := range []string{"delete", "type", "target", "collector"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t, "0s", "process_absent", 0)
			f.status(t, collector.StateDown)
			alert := takeAlert(t, f.notifications, StateFiring)
			f.now = f.now.Add(time.Second)
			switch change {
			case "delete":
				f.cfg.Rules = nil
			case "type":
				port := 3001
				f.cfg.Targets[0].Match.Port = &port
				f.cfg.Rules[0].Type = "port_closed"
			case "target":
				f.cfg.Targets = append(f.cfg.Targets, config.Target{ID: "other", Match: config.Match{Process: "other.exe"}})
				f.cfg.Rules[0].Target = "other"
			case "collector":
				f.cfg.Targets[0].Collector = "other-collector"
			}
			if err := f.engine.UpdateConfigContext(context.Background(), f.cfg); err != nil {
				t.Fatal(err)
			}
			closed := f.repository.alerts[alert.ID]
			if closed.State != StateResolved || closed.ResolvedAt != f.now.UnixMilli() || len(f.repository.events) != 2 || f.repository.events[1].Kind != "closed" || f.repository.events[1].Message != closedMessage {
				t.Fatalf("rule edit did not close the old incident: %+v %+v", closed, f.repository.events)
			}
			noNotification(t, f.notifications)
		})
	}
}

func TestRulesOnlyEvaluateAssignedCollectorIncludingDelayedOldMessages(t *testing.T) {
	f := newFixture(t, "0s", "process_absent", 0)
	f.cfg.Targets[0].Collector = "REMOTE-HOST"
	opts := f.options()
	hostname := "other-host"
	opts.CollectorHostname = func(context.Context, string) (string, error) { return hostname, nil }
	var err error
	f.engine, err = New(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, collector.StateDown)
	noNotification(t, f.notifications)
	hostname = "remote-host"
	f.status(t, collector.StateDown)
	alert := takeAlert(t, f.notifications, StateFiring)
	f.cfg.Targets[0].Collector = "unassigned-host"
	if err := f.engine.UpdateConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	if got := f.repository.alerts[alert.ID]; got.State != StateResolved {
		t.Fatalf("assignment change kept firing alert: %+v", got)
	}
	f.status(t, collector.StateDown)
	f.evaluate(t, time.Minute)
	noNotification(t, f.notifications)
	f.cfg.Targets[0].Collector = "REMOTE-HOST"
	if err := f.engine.UpdateConfig(f.cfg); err != nil {
		t.Fatal(err)
	}
	f.evaluate(t, time.Minute)
	noNotification(t, f.notifications)
	f.status(t, collector.StateDown)
	if again := takeAlert(t, f.notifications, StateFiring); again.ID == alert.ID {
		t.Fatal("new assignment reused closed incident")
	}
}

func TestRestartClosesFiringAlertsWhoseRuleWasRemoved(t *testing.T) {
	f := newFixture(t, "0s", "process_absent", 0)
	f.status(t, collector.StateDown)
	alert := takeAlert(t, f.notifications, StateFiring)
	f.cfg.Rules = nil
	f.now = f.now.Add(2 * time.Second)
	queue := &notificationQueue{alerts: make(chan Alert, 10)}
	opts := f.options()
	opts.Notifications = queue
	restarted, err := New(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.repository.alerts[alert.ID]; got.State != StateResolved || got.ResolvedAt != f.now.UnixMilli() || f.repository.events[len(f.repository.events)-1].Kind != "closed" || len(restarted.Snapshot()) != 0 {
		t.Fatalf("restart left an orphaned alert: %+v", got)
	}
	noNotification(t, queue)
}

func TestRetirementSaveFailureRetriesWithoutSendingRecovery(t *testing.T) {
	f := newFixture(t, "0s", "process_absent", 0)
	f.status(t, collector.StateDown)
	alert := takeAlert(t, f.notifications, StateFiring)
	f.cfg.Rules = nil
	f.repository.saveErr = errors.New("controlled closure failure")
	if err := f.engine.UpdateConfig(f.cfg); !errors.Is(err, f.repository.saveErr) {
		t.Fatalf("closure failure was hidden: %v", err)
	}
	if f.repository.alerts[alert.ID].State != StateFiring {
		t.Fatal("failed closure changed persisted state")
	}
	f.repository.saveErr = nil
	f.evaluate(t, time.Second)
	if f.repository.alerts[alert.ID].State != StateResolved || f.repository.events[len(f.repository.events)-1].Kind != "closed" || len(f.engine.Snapshot()) != 0 {
		t.Fatal("retired state did not retry closure")
	}
	noNotification(t, f.notifications)
}
