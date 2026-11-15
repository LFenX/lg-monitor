package app

import (
	"context"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func TestIncidentsGroupOutagesOfTheServiceAndItsChildren(t *testing.T) {
	f := newAPIFixture(t)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构网站","id":"site","process":"fake-site.exe"}`, 200)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构隧道","id":"tunnel","process":"fake-tunnel.exe","parent":"site"}`, 200)
	seq := uint64(0)
	write := func(target, state, reason string, offset time.Duration) {
		t.Helper()
		seq++
		process := collector.ProcessFound
		if state == "down" {
			process = collector.ProcessMissing
		}
		message := pipeline.Message{CollectorID: "fake-host", Seq: seq, TS: f.now.Add(offset).UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: target, State: state, Process: process, Reason: reason}}
		if err := f.db.Write(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	// The site was down for four minutes an hour ago and blinked for twenty seconds half an hour ago.
	for offset := -70 * time.Minute; offset <= 0; offset += 10 * time.Second {
		state := "up"
		if (offset >= -60*time.Minute && offset < -56*time.Minute) || (offset >= -30*time.Minute && offset < -29*time.Minute-40*time.Second) {
			state = "down"
		}
		write("site", state, map[bool]string{true: "虚构进程未找到", false: "进程运行正常"}[state == "down"], offset)
	}
	// The tunnel has been failing for forty seconds and still is: it shows at once, before the minute passes.
	for offset := -70 * time.Minute; offset <= 0; offset += 10 * time.Second {
		write("tunnel", map[bool]string{true: "down", false: "up"}[offset >= -40*time.Second], "虚构隧道断开", offset)
	}
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	during := rules.Alert{ID: "fake-incident-alert", RuleID: "site-process-down", CollectorID: "fake-host", TargetID: "site", State: rules.StateFiring, Severity: "critical", Summary: "虚构网站进程已停止", StartedAt: f.now.Add(-59 * time.Minute).UnixMilli()}
	alone := rules.Alert{ID: "fake-certificate-alert", RuleID: "site-cert-expiry", CollectorID: "fake-host", TargetID: "site", State: rules.StateFiring, Severity: "warning", Summary: "虚构证书 10 天后到期", StartedAt: f.now.Add(-2 * time.Hour).UnixMilli()}
	for _, alert := range []rules.Alert{during, alone} {
		if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, TS: alert.StartedAt, Kind: rules.StateFiring, Message: alert.Summary}); err != nil {
			t.Fatal(err)
		}
	}
	during.State, during.ResolvedAt = rules.StateResolved, f.now.Add(-56*time.Minute).UnixMilli()
	if err := f.db.SaveTransition(context.Background(), during, rules.Event{AlertID: during.ID, TS: during.ResolvedAt, Kind: rules.StateResolved, Message: "虚构恢复"}); err != nil {
		t.Fatal(err)
	}
	detail := decodeAs[api.MonitorDetail](t, f.request(t, "GET", "/api/v1/monitors/site", "read", "", 200).Body.Bytes())
	var site, tunnel, alerts []api.MonitorIncident
	for _, item := range detail.Incidents {
		switch {
		case item.Kind == "alert":
			alerts = append(alerts, item)
		case item.MonitorID == "site":
			site = append(site, item)
		case item.MonitorID == "tunnel":
			tunnel = append(tunnel, item)
		}
	}
	if len(site) != 1 {
		t.Fatalf("site outages=%+v (the twenty-second blink must not count)", site)
	}
	outage := site[0]
	if outage.From != f.now.Add(-60*time.Minute).UnixMilli() || outage.To != f.now.Add(-56*time.Minute).UnixMilli() || outage.DurationMS != (4*time.Minute).Milliseconds() {
		t.Fatalf("outage times=%+v", outage)
	}
	if outage.MonitorName != "虚构网站" || outage.Depth != 0 || outage.Reason != "虚构进程未找到" || outage.Host == "" {
		t.Fatalf("outage facts=%+v", outage)
	}
	if len(outage.Alerts) != 1 || outage.Alerts[0].Summary != "虚构网站进程已停止" || outage.Alerts[0].ResolvedAt == 0 {
		t.Fatalf("alert during the outage=%+v", outage.Alerts)
	}
	if len(tunnel) != 1 || tunnel[0].To != 0 || tunnel[0].Depth != 1 || tunnel[0].MonitorName != "虚构隧道" || tunnel[0].DurationMS != (40*time.Second).Milliseconds() {
		t.Fatalf("ongoing child outage=%+v", tunnel)
	}
	if len(alerts) != 1 || alerts[0].Reason != "虚构证书 10 天后到期" || alerts[0].To != 0 {
		t.Fatalf("standalone alerts=%+v", alerts)
	}
	// Newest first across the whole group.
	for index := 1; index < len(detail.Incidents); index++ {
		if detail.Incidents[index-1].From < detail.Incidents[index].From {
			t.Fatalf("not newest first: %+v", detail.Incidents)
		}
	}
	// The child's own page lists only itself.
	child := decodeAs[api.MonitorDetail](t, f.request(t, "GET", "/api/v1/monitors/tunnel", "read", "", 200).Body.Bytes())
	for _, item := range child.Incidents {
		if item.MonitorID != "tunnel" || item.Depth != 0 {
			t.Fatalf("child page leaked other services: %+v", child.Incidents)
		}
	}
}
