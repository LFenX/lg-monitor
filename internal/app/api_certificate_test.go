package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

func seedCertificateAPI(t *testing.T, f *apiFixture) {
	t.Helper()
	f.samples(t)
	for index, days := range []int{3, -1} {
		at := f.now.Add(-time.Second).UnixMilli()
		expires := f.now.Add(time.Duration(days) * 24 * time.Hour).UnixMilli()
		message := pipeline.Message{CollectorID: "fake-host", Seq: uint64(100 + index), TS: at, Kind: pipeline.KindProbeResult,
			ProbeResult: &pipeline.ProbeResult{TargetID: "web", Probe: index, Type: "http", TS: at, OK: days > 0, TLSNotAfter: expires, TLSSubject: []string{"fake leaf", "fake intermediate"}[index]}}
		if err := f.db.Write(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDelayedCertificateNotificationKeepsEventMetadataAfterNewObservation(t *testing.T) {
	now := time.UnixMilli(2000000000000)
	certificate := &rules.Certificate{NotAfter: now.Add(3 * 24 * time.Hour).UnixMilli(), Subject: "old certificate"}
	alert := rules.Alert{ID: "fake", RuleID: "cert", CollectorID: "fake", TargetID: "web", State: rules.StateFiring, Summary: certificate.Message(now), Cert: certificate, NotificationKey: "resume:fake"}
	queue := &notificationQueue{active: &config.Config{
		Targets:   []config.Target{{ID: "web"}},
		Rules:     []config.Rule{{ID: "cert", Type: "cert_expiry", Target: "web", Notify: []string{"fake"}}},
		Notifiers: []config.Notifier{{ID: "fake", Type: "toast"}},
	}, now: func() time.Time { return now },
		targets: map[targetKey]pipeline.TargetStatus{{"fake", "web"}: {Reason: "process is healthy"}},
		probes:  map[targetKey]map[int]pipeline.ProbeResult{{"fake", "web"}: {0: {TLSNotAfter: now.Add(90 * 24 * time.Hour).UnixMilli(), TLSSubject: "renewed certificate"}}},
	}
	job, err := queue.job(alert)
	if err != nil || job.input.Alert.Cert != certificate || job.input.Value != certificate.Message(now) ||
		job.input.Detail != "通知暂停期间发生，恢复通知后补发；"+alert.Summary {
		t.Fatalf("latest observation overwrote delayed certificate: %+v %v", job.input, err)
	}
}

func TestHTTPCertificateHistoryMetadataIsReadableAndMatchesSchema(t *testing.T) {
	f := newAPIFixture(t)
	seedCertificateAPI(t, f)
	response := f.request(t, "GET", "/api/v1/targets/fake-host/web/history", "read", "", 200).Body.Bytes()
	var history store.TargetHistoryResult
	if err := json.Unmarshal(response, &history); err != nil || len(history.Certificates) != 2 || history.Certificates[0].TLSNotAfter != f.now.Add(3*24*time.Hour).UnixMilli() {
		t.Fatalf("certificate metadata missing: %s %v", response, err)
	}
}
