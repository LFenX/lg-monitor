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
	for _, change := range []string{"delete", "type", "target"} {
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
