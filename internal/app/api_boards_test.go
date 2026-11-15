package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func decodeAs[T any](t *testing.T, data []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	return value
}

func (f *apiFixture) status(t *testing.T, seq uint64, target, state string, age time.Duration) {
	t.Helper()
	process := collector.ProcessFound
	if state == "down" {
		process = collector.ProcessMissing
	}
	message := pipeline.Message{CollectorID: "fake-host", Seq: seq, TS: f.now.Add(-age).UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: target, State: state, Process: process}}
	if err := f.db.Write(context.Background(), message); err != nil {
		t.Fatal(err)
	}
}

func TestBoardsGroupChildServicesUnderTheirParent(t *testing.T) {
	f := newAPIFixture(t)
	f.samples(t)
	// The first monitor on a fresh install creates the default board and brings older monitors along.
	site := decodeAs[api.MonitorResult](t, f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构网站","id":"site","port":3000}`, 200).Body.Bytes())
	if site.Board != "main" || !site.BoardCreated || !strings.HasSuffix(site.BoardURL, "/#/boards/main") {
		t.Fatalf("site=%+v", site)
	}
	board, ok := f.manager.Snapshot().FindBoard("main")
	if !ok || board.Name != "我的服务" || !slices.Equal(board.Monitors, []string{"web", "site"}) {
		t.Fatalf("default board=%+v", board)
	}
	tunnel := decodeAs[api.MonitorResult](t, f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构隧道","id":"tunnel","service":"fake-tunnel","parent":"site"}`, 200).Body.Bytes())
	if tunnel.Board != "main" || tunnel.BoardCreated || tunnel.Monitor.Parent != "site" {
		t.Fatalf("tunnel=%+v", tunnel)
	}
	// A child is shown under its parent, so it is not listed on the board itself.
	if board, _ := f.manager.Snapshot().FindBoard("main"); slices.Contains(board.Monitors, "tunnel") {
		t.Fatalf("child must not be listed separately: %+v", board)
	}
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构网关","id":"gateway","process":"fake-gateway.exe","parent":"site"}`, 200)

	f.status(t, 30, "site", "up", 2*time.Second)
	f.status(t, 31, "tunnel", "down", 2*time.Second)
	f.status(t, 32, "gateway", "up", 2*time.Second)
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.backend.agent.history, f.backend.agent.listed = nil, nil
	view := decodeAs[api.BoardView](t, f.request(t, "GET", "/api/v1/boards/main", "read", "", 200).Body.Bytes())
	var order []string
	for _, item := range view.Items {
		order = append(order, strings.Repeat(">", item.Depth)+item.ID)
	}
	if strings.Join(order, ",") != "web,site,>tunnel,>gateway" {
		t.Fatalf("display order=%v", order)
	}
	parent := view.Items[1]
	// The fixture's second collector has no samples, so the parent itself is only "unknown".
	if parent.State != "unknown" || parent.Overall != "down" || parent.Issue != "子服务 虚构隧道 异常" || !slices.Equal(parent.Children, []string{"tunnel", "gateway"}) || len(parent.GroupBeats) != len(parent.Beats) || parent.GroupBeats[len(parent.GroupBeats)-1] != "down" {
		t.Fatalf("parent=%+v", parent)
	}
	if view.Summary.Services != 2 || view.Summary.Children != 2 || view.Summary.Down != 1 || view.Summary.State != "down" || len(view.Unassigned) != 0 {
		t.Fatalf("summary=%+v unassigned=%v", view.Summary, view.Unassigned)
	}
	boards := decodeAs[[]api.BoardInfo](t, f.request(t, "GET", "/api/v1/boards", "read", "", 200).Body.Bytes())
	if len(boards) != 1 || boards[0].Summary.Children != 2 {
		t.Fatalf("boards=%+v", boards)
	}

	// Loops and deep nesting are refused before anything is written.
	for _, step := range []struct{ body, want string }{
		{`{"name":"虚构网站","id":"site","port":3000,"parent":"tunnel"}`, "子服务"},
		{`{"name":"自己","id":"self","port":3001,"parent":"self"}`, "自己"},
		{`{"name":"没有父","id":"orphan","port":3002,"parent":"ghost"}`, "ghost 不存在"},
		{`{"name":"放错面板","id":"lost","port":3003,"board":"nowhere"}`, "nowhere 不存在"},
		{`{"name":"第三层","id":"deep","port":3004,"parent":"tunnel"}`, ""},
		{`{"name":"第四层","id":"deeper","port":3005,"parent":"deep"}`, "最多三层"},
		{`{"name":"虚构网站","id":"site","port":3000,"parent":"deep"}`, "子服务"},
		{`{"name":"虚构网关","id":"gateway","process":"fake-gateway.exe"}`, ""},
		{`{"name":"虚构网关","id":"gateway","process":"fake-gateway.exe","parent":""}`, ""},
	} {
		status := 400
		if step.want == "" {
			status = 200
		}
		response := f.request(t, "POST", "/api/v1/monitors", "config", step.body, status)
		if !strings.Contains(response.Body.String(), step.want) {
			t.Fatalf("%s: %s", step.body, response.Body.String())
		}
	}
	targets := targetIndex(f.manager.Snapshot())
	if targets["gateway"].Parent != "" || targets["deep"].Parent != "tunnel" {
		t.Fatalf("omitting parent keeps it, an empty string clears it: %+v %+v", targets["gateway"], targets["deep"])
	}

	// Another board can show a single child on its own.
	created := decodeAs[api.BoardView](t, f.request(t, "POST", "/api/v1/boards", "config", `{"name":"边缘","description":"只看隧道"}`, 200).Body.Bytes())
	if created.ID != "board-1" || len(created.Monitors) != 0 || created.Description != "只看隧道" {
		t.Fatalf("created=%+v", created)
	}
	f.request(t, "POST", "/api/v1/boards", "read", `{"name":"无权"}`, 403)
	f.request(t, "POST", "/api/v1/boards", "config", `{"name":"重复","id":"main"}`, 409)
	f.request(t, "POST", "/api/v1/boards", "config", `{"name":"  "}`, 400)
	if missing := f.request(t, "PUT", "/api/v1/boards/board-1", "config", `{"name":"边缘服务","add":["tunnel","ghost"]}`, 400); !strings.Contains(missing.Body.String(), "ghost") {
		t.Fatal(missing.Body.String())
	}
	edge := decodeAs[api.BoardView](t, f.request(t, "PUT", "/api/v1/boards/board-1", "config", `{"name":"边缘服务","add":["tunnel"]}`, 200).Body.Bytes())
	if edge.Name != "边缘服务" || len(edge.Items) != 2 || edge.Items[0].ID != "tunnel" || edge.Items[1].Depth != 1 {
		t.Fatalf("edge=%+v", edge.Items)
	}

	// Removing a parent needs explicit consent for its children, then takes them off every board.
	refused := f.request(t, "DELETE", "/api/v1/monitors/site", "config", "", 409)
	if !strings.Contains(refused.Body.String(), "虚构隧道") || !strings.Contains(refused.Body.String(), "children=true") {
		t.Fatal(refused.Body.String())
	}
	f.request(t, "DELETE", "/api/v1/monitors/site?children=true", "config", "", 200)
	snapshot := f.manager.Snapshot()
	for _, id := range []string{"site", "tunnel", "deep"} {
		if _, ok := targetIndex(snapshot)[id]; ok {
			t.Fatalf("%s survived the group removal", id)
		}
		if slices.ContainsFunc(snapshot.Rules, func(rule config.Rule) bool { return rule.Target == id }) {
			t.Fatalf("rules for %s survived", id)
		}
	}
	if board, _ := snapshot.FindBoard("board-1"); len(board.Monitors) != 0 {
		t.Fatalf("removed child still listed: %+v", board)
	}
	// Deleting a board keeps its monitors; they show up as unassigned.
	f.request(t, "DELETE", "/api/v1/boards/main", "config", "", 200)
	remaining := decodeAs[api.BoardView](t, f.request(t, "GET", "/api/v1/boards/board-1", "read", "", 200).Body.Bytes())
	if !slices.Equal(remaining.Unassigned, []string{"web", "gateway"}) {
		t.Fatalf("unassigned=%v", remaining.Unassigned)
	}
	f.request(t, "GET", "/api/v1/boards/main", "read", "", 404)
}
