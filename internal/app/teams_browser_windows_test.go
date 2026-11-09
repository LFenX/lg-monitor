//go:build windows && integration && webui

package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestNativeTeamsBrowser(t *testing.T) {
	when := time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)
	start := time.Now()
	elapsed := func() time.Duration { return time.Since(start) }
	owner := newTeamFixtureWithElapsed(t, newAPIFixtureWithHTTPClock(t, when, time.Now), elapsed)
	member := newTeamFixtureWithElapsed(t, newAPIFixtureWithHTTPClock(t, when, time.Now), elapsed)
	loadAPIDashboard(t, owner.f, teamDashboard)
	owner.f.samples(t)
	member.f.samples(t)
	createFixtureTeam(t, owner)
	ctx := context.Background()
	if err := owner.f.db.RegisterCollector(ctx, store.CollectorRecord{ID: "outside-host", Hostname: "未分享主机", FirstSeen: 1, LastSeen: when.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err := owner.f.db.Write(ctx, pipeline.Message{CollectorID: "outside-host", Seq: 1, TS: when.UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: "down"}}); err != nil {
		t.Fatal(err)
	}
	if err := owner.f.db.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	ownerServer := httptest.NewServer(owner.f.server)
	defer ownerServer.Close()
	t.Setenv("LGMON_BROWSER_OWNER_URL", ownerServer.URL)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__test/teams/advance" {
			member.f.server.ServeHTTP(w, r)
			return
		}
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fake-test-control" {
			http.Error(w, "forbidden", 403)
			return
		}
		owner.clock.Add(11000)
		member.clock.Add(11000)
		w.WriteHeader(http.StatusNoContent)
	})
	runNativeWebBrowser(t, member.f, "teams.spec.ts", handler)
	if len(owner.f.client.sent) != 0 || len(member.f.client.sent) != 0 || owner.f.files.writes != 0 || member.f.files.writes != 0 {
		t.Fatal("team browser changed monitoring configuration or sent notifications")
	}
}
