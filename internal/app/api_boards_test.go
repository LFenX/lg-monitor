// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

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

func TestMonitorTreeKeepsEachProbeIndependent(t *testing.T) {
	cfg := &config.Config{Targets: []config.Target{{ID: "root"}, {ID: "middle", Parent: "root"}, {ID: "leaf", Parent: "middle"}}}
	for _, states := range [][3]string{{"up", "up", "down"}, {"up", "unknown", "down"}, {"down", "up", "up"}, {"unknown", "down", "unknown"}} {
		items := []api.Monitor{{ID: "root", State: states[0]}, {ID: "middle", State: states[1]}, {ID: "leaf", State: states[2]}}
		for i := range items {
			items[i].Beats = []string{states[i]}
			if states[i] == "down" {
				items[i].Issue = items[i].ID + " failed"
			}
		}
		decorate(cfg, items, "")
		for i, item := range items {
			issue := ""
			if states[i] == "down" {
				issue = item.ID + " failed"
			}
			if item.State != states[i] || item.Overall != states[i] || item.Beats[0] != states[i] || item.Issue != issue {
				t.Fatalf("states %v: %+v", states, item)
			}
		}
	}
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
	if parent.State != "unknown" || parent.Overall != parent.State || parent.Issue != "" || !slices.Equal(parent.Children, []string{"tunnel", "gateway"}) || len(parent.GroupBeats) != len(parent.Beats) || parent.GroupBeats[len(parent.GroupBeats)-1] != "up" || len(parent.GroupBeatSlots) != 48 {
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

func TestEditMonitorRenamesAndMovesWithoutTouchingChecks(t *testing.T) {
	f := newAPIFixture(t)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构网站","id":"site","port":3000}`, 200)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构隧道","id":"tunnel","service":"fake-tunnel"}`, 200)
	find := func(id string) config.Target {
		for _, target := range f.manager.Snapshot().Targets {
			if target.ID == id {
				return target
			}
		}
		t.Fatalf("target %s missing", id)
		return config.Target{}
	}
	before := find("tunnel")
	moved := decodeAs[api.Monitor](t, f.request(t, "PUT", "/api/v1/monitors/tunnel", "config", `{"name":"  外网隧道 ","parent":"site"}`, 200).Body.Bytes())
	if moved.Name != "外网隧道" || moved.Parent != "site" {
		t.Fatalf("moved=%+v", moved)
	}
	after := find("tunnel")
	if after.Match.Service != before.Match.Service || len(after.Probes) != len(before.Probes) {
		t.Fatalf("checks changed: before=%+v after=%+v", before, after)
	}
	rules := 0
	for _, rule := range f.manager.Snapshot().Rules {
		if rule.Target == "tunnel" {
			rules++
		}
	}
	if rules == 0 {
		t.Fatal("editing removed the monitor's rules")
	}
	// Back to a main service with an empty parent; bad input is refused without writing.
	if back := decodeAs[api.Monitor](t, f.request(t, "PUT", "/api/v1/monitors/tunnel", "config", `{"parent":""}`, 200).Body.Bytes()); back.Parent != "" || back.Name != "外网隧道" {
		t.Fatalf("back=%+v", back)
	}
	f.request(t, "PUT", "/api/v1/monitors/tunnel", "config", `{"name":"  "}`, 400)
	f.request(t, "PUT", "/api/v1/monitors/tunnel", "config", `{"parent":"tunnel"}`, 400)
	f.request(t, "PUT", "/api/v1/monitors/site", "config", `{"parent":"ghost"}`, 400)
	f.request(t, "PUT", "/api/v1/monitors/ghost", "config", `{"name":"x"}`, 404)
	f.request(t, "PUT", "/api/v1/monitors/site", "read", `{"name":"x"}`, 403)
}

func TestBoardCardStylesAreValidatedAndCarryTheirFigures(t *testing.T) {
	f := newAPIFixture(t)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构网站","id":"site","port":3000}`, 200)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构网关","id":"gateway","url":"https://fake.invalid/health"}`, 200)
	view := decodeAs[api.BoardView](t, f.request(t, "GET", "/api/v1/boards/main", "read", "", 200).Body.Bytes())
	if view.CardStyle != "pulse" || len(view.Cards) != 0 {
		t.Fatalf("default style=%q cards=%v", view.CardStyle, view.Cards)
	}
	for _, item := range view.Items {
		if item.Days != nil || item.Latency != nil {
			t.Fatalf("pulse cards must not pay for 30-day figures: %+v", item)
		}
	}
	probe := pipeline.Message{CollectorID: "fake-host", Seq: 900, TS: f.now.Add(-5 * time.Minute).UnixMilli(), Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "gateway", Probe: 0, Type: "http", OK: true, LatencyMS: 42}}
	if err := f.db.Write(context.Background(), probe); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	view = decodeAs[api.BoardView](t, f.request(t, "PUT", "/api/v1/boards/main", "config", `{"cardStyle":"ring","cards":{"gateway":"latency"}}`, 200).Body.Bytes())
	if view.CardStyle != "ring" || view.Cards["gateway"] != "latency" {
		t.Fatalf("styles not saved: %q %v", view.CardStyle, view.Cards)
	}
	for _, item := range view.Items {
		switch item.ID {
		case "site":
			if len(item.Days) != 30 || item.Latency != nil {
				t.Fatalf("ring card figures=%+v", item)
			}
		case "gateway":
			if len(item.Latency) != 1 || item.Latency[0].MS != 42 || item.Days != nil {
				t.Fatalf("latency card figures=%+v", item)
			}
		}
	}
	board, _ := f.manager.Snapshot().FindBoard("main")
	if board.StyleOf("site") != "ring" || board.StyleOf("gateway") != "latency" {
		t.Fatalf("stored board=%+v", board)
	}
	// Picking the board's default for one service stores nothing for it.
	f.request(t, "PUT", "/api/v1/boards/main", "config", `{"cards":{"site":"ring"}}`, 200)
	if board, _ := f.manager.Snapshot().FindBoard("main"); board.Cards["site"] != "" {
		t.Fatalf("a style equal to the default was stored: %+v", board.Cards)
	}
	// An empty value goes back to the board's default; choosing the default style stores nothing.
	f.request(t, "PUT", "/api/v1/boards/main", "config", `{"cardStyle":"pulse","cards":{"gateway":""}}`, 200)
	if board, _ := f.manager.Snapshot().FindBoard("main"); board.CardStyle != "" || board.Cards != nil {
		t.Fatalf("defaults should leave nothing behind: %+v", board)
	}
	if bad := f.request(t, "PUT", "/api/v1/boards/main", "config", `{"cardStyle":"sparkles"}`, 400); !strings.Contains(bad.Body.String(), "sparkles") {
		t.Fatal(bad.Body.String())
	}
	if bad := f.request(t, "PUT", "/api/v1/boards/main", "config", `{"cards":{"ghost":"ring"}}`, 400); !strings.Contains(bad.Body.String(), "ghost") {
		t.Fatal(bad.Body.String())
	}
	f.request(t, "PUT", "/api/v1/boards/main", "config", `{"cards":{"site":"glitter"}}`, 400)
	f.request(t, "PUT", "/api/v1/boards/main", "read", `{"cardStyle":"ring"}`, 403)
}

func TestWebServicesHaveAnEntryPeopleCanOpen(t *testing.T) {
	f := newAPIFixture(t)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构博客","id":"blog","port":3000,"role":"web"}`, 200)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构网站","id":"site","url":"https://example.com/health"}`, 200)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构订单库","id":"db","port":5432,"role":"database"}`, 200)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构控制台","id":"console","port":8443,"role":"app","entry":"https://console.example.com"}`, 200)
	entries := func() map[string]api.Monitor {
		result := map[string]api.Monitor{}
		for _, item := range decodeAs[[]api.Monitor](t, f.request(t, "GET", "/api/v1/monitors", "read", "", 200).Body.Bytes()) {
			result[item.ID] = item
		}
		return result
	}
	got := entries()
	// A web port opens on this computer, a site at its own address, a set entry as written; a database port is no page.
	if got["blog"].Entry != "http://127.0.0.1:3000" || !got["blog"].EntryGuessed ||
		got["site"].Entry != "https://example.com/health" || !got["site"].EntryGuessed ||
		got["db"].Entry != "" || got["console"].Entry != "https://console.example.com" || got["console"].EntryGuessed {
		t.Fatalf("entries=%+v %+v %+v %+v", got["blog"], got["site"], got["db"], got["console"])
	}
	edited := decodeAs[api.Monitor](t, f.request(t, "PUT", "/api/v1/monitors/blog", "config", `{"entry":"  https://blog.example.com  "}`, 200).Body.Bytes())
	if edited.Entry != "https://blog.example.com" || edited.EntryGuessed {
		t.Fatalf("edited=%+v", edited)
	}
	// Re-adding the monitor keeps the entry; an empty one goes back to the guess.
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构博客","id":"blog","port":3001,"role":"web"}`, 200)
	if entry := targetIndex(f.manager.Snapshot())["blog"].Entry; entry != "https://blog.example.com" {
		t.Fatalf("update dropped the entry: %q", entry)
	}
	f.request(t, "PUT", "/api/v1/monitors/blog", "config", `{"entry":""}`, 200)
	if got = entries(); got["blog"].Entry != "http://127.0.0.1:3001" || !got["blog"].EntryGuessed {
		t.Fatalf("cleared=%+v", got["blog"])
	}
	for _, bad := range []string{`javascript:alert(1)`, `ftp://files.example.com`, `https://user:secret@example.com`, `example.com`} {
		if response := f.request(t, "PUT", "/api/v1/monitors/blog", "config", `{"entry":"`+bad+`"}`, 400); !strings.Contains(response.Body.String(), "访问入口") {
			t.Fatalf("%s: %s", bad, response.Body.String())
		}
	}
	f.request(t, "PUT", "/api/v1/monitors/blog", "read", `{"entry":"https://blog.example.com"}`, 403)
	// A colleague on the LAN cannot open the owner's 127.0.0.1, but a public address still helps them.
	view := sanitizeBoard(api.BoardView{Items: []api.Monitor{got["blog"], got["console"]}})
	if view.Items[0].Entry != "" || view.Items[1].Entry != "https://console.example.com" {
		t.Fatalf("shared entries=%q %q", view.Items[0].Entry, view.Items[1].Entry)
	}
}

func TestServicesDescribeThemselvesAndTheirDependencies(t *testing.T) {
	f := newAPIFixture(t)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构网站","id":"site","port":3000,"description":"  客服每天用的前端  "}`, 200)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构订单库","id":"orders-db","service":"postgresql-x64-16"}`, 200)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构隧道","id":"tunnel","service":"fake-cloudflared","role":"tunnel"}`, 200)
	find := func(list []api.Monitor, id string) api.Monitor {
		for _, item := range list {
			if item.ID == id {
				return item
			}
		}
		t.Fatalf("%s missing", id)
		return api.Monitor{}
	}
	edited := decodeAs[api.Monitor](t, f.request(t, "PUT", "/api/v1/monitors/site", "config",
		`{"role":"web","depends":[{"target":"orders-db","relation":"data","note":"订单表"},{"target":"tunnel","relation":"via"}]}`, 200).Body.Bytes())
	if edited.Role != "web" || edited.RoleGuessed || edited.Description != "客服每天用的前端" || len(edited.Depends) != 2 ||
		edited.Depends[0] != (api.MonitorDependency{ID: "orders-db", Relation: "data", Note: "订单表"}) {
		t.Fatalf("edited=%+v", edited)
	}
	list := decodeAs[[]api.Monitor](t, f.request(t, "GET", "/api/v1/monitors", "read", "", 200).Body.Bytes())
	// The database's role is guessed from what it watches; it knows who uses it.
	if db := find(list, "orders-db"); db.Role != "database" || !db.RoleGuessed || len(db.Dependents) != 1 || db.Dependents[0].ID != "site" || db.Dependents[0].Relation != "data" {
		t.Fatalf("db=%+v", db)
	}
	// Updating a monitor without the fields keeps them; "uses" is the default relation and is stored as nothing.
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构网站","id":"site","port":3001}`, 200)
	site := targetIndex(f.manager.Snapshot())["site"]
	if site.Description != "客服每天用的前端" || site.Role != "web" || len(site.Depends) != 2 {
		t.Fatalf("update dropped the description: %+v", site)
	}
	f.request(t, "PUT", "/api/v1/monitors/tunnel", "config", `{"depends":[{"target":"orders-db","relation":"uses"}],"role":""}`, 200)
	tunnel := targetIndex(f.manager.Snapshot())["tunnel"]
	if tunnel.Role != "" || len(tunnel.Depends) != 1 || tunnel.Depends[0].Relation != "" {
		t.Fatalf("tunnel=%+v", tunnel)
	}
	for body, want := range map[string]string{
		`{"depends":[{"target":"site"}]}`:                       "自己",
		`{"depends":[{"target":"ghost"}]}`:                      "ghost",
		`{"depends":[{"target":"tunnel"},{"target":"tunnel"}]}`: "两次",
		`{"depends":[{"target":"tunnel","relation":"owns"}]}`:   "relation",
		`{"role":"spaceship"}`:                                  "role",
		`{"description":"` + strings.Repeat("长", 301) + `"}`:    "300",
	} {
		if response := f.request(t, "PUT", "/api/v1/monitors/site", "config", body, 400); !strings.Contains(response.Body.String(), want) {
			t.Fatalf("%s: %s", body, response.Body.String())
		}
	}
	// Documents: read scope reads, config writes, empty deletes, oversized and unknown services are refused.
	f.request(t, "PUT", "/api/v1/monitors/orders-db/doc", "config", `{"markdown":"## 备份\n每天 01:00 备份到 [[tunnel]] 另一头。"}`, 200)
	if doc := decodeAs[api.MonitorDoc](t, f.request(t, "GET", "/api/v1/monitors/orders-db/doc", "read", "", 200).Body.Bytes()); !strings.Contains(doc.Markdown, "[[tunnel]]") {
		t.Fatalf("doc=%+v", doc)
	}
	f.request(t, "PUT", "/api/v1/monitors/orders-db/doc", "read", `{"markdown":"x"}`, 403)
	f.request(t, "PUT", "/api/v1/monitors/ghost/doc", "config", `{"markdown":"x"}`, 404)
	f.request(t, "GET", "/api/v1/monitors/ghost/doc", "read", "", 404)
	f.request(t, "PUT", "/api/v1/monitors/site/doc", "config", `{"markdown":"`+strings.Repeat("长", 90000)+`"}`, 400)
	f.request(t, "PUT", "/api/v1/monitors/site/doc", "config", `{"markdown":"临时"}`, 200)
	f.request(t, "PUT", "/api/v1/monitors/site/doc", "config", `{"markdown":"  "}`, 200)
	if _, ok := f.manager.Snapshot().Docs["site"]; ok {
		t.Fatal("an empty document was kept")
	}
	// Removing a service takes the lines pointing at it, and its document, too.
	f.request(t, "DELETE", "/api/v1/monitors/orders-db", "config", "", 200)
	if _, ok := f.manager.Snapshot().Docs["orders-db"]; ok {
		t.Fatal("the removed service's document stayed")
	}
	for _, target := range f.manager.Snapshot().Targets {
		if slices.ContainsFunc(target.Depends, func(item config.Dependency) bool { return item.Target == "orders-db" }) {
			t.Fatalf("%s still depends on the removed service", target.ID)
		}
	}
	if site := targetIndex(f.manager.Snapshot())["site"]; len(site.Depends) != 1 || site.Depends[0].Target != "tunnel" {
		t.Fatalf("site=%+v", site.Depends)
	}
}

func TestServiceRolesAreGuessedFromWhatTheyWatch(t *testing.T) {
	port := 3029
	for target, want := range map[*config.Target]string{
		{ID: "a", Name: "MCP 文档服务（本机 3029）", Match: config.Match{Port: &port}}:       "mcp",
		{ID: "b", Name: "AICS生产端数据库连接通道", Match: config.Match{Process: "lgmon.exe"}}: "tunnel",
		{ID: "c", Name: "缓存", Match: config.Match{Service: "Redis"}}:                 "cache",
		{ID: "d", Name: "博客", Match: config.Match{Port: &port}}:                      "web",
		{ID: "g", Name: "youtube_clawler", Match: config.Match{Port: &port}}:         "worker",
		{ID: "h", Name: "AICS服务器压力监控台", Match: config.Match{Port: &port}}:            "web",
		{ID: "i", Name: "订单接口", Match: config.Match{Port: &port}}:                    "api",
		{ID: "e", Name: "夜间同步", Task: `\Fake\Nightly`}:                               "scheduler",
		{ID: "f", Name: "随便", Role: "external"}:                                      "external",
	} {
		if role, _ := serviceRole(*target); role != want {
			t.Errorf("%s: role=%s want %s", target.Name, role, want)
		}
	}
}

func TestSharedBoardKeepsOnlyRelationsBetweenSharedServices(t *testing.T) {
	view := sanitizeBoard(api.BoardView{Items: []api.Monitor{
		{ID: "site", Depends: []api.MonitorDependency{{ID: "orders-db", Relation: "data"}, {ID: "private-vault", Relation: "auth"}}},
		{ID: "orders-db", Dependents: []api.MonitorDependency{{ID: "site", Relation: "data"}, {ID: "private-report", Relation: "reads"}}},
	}})
	if len(view.Items[0].Depends) != 1 || view.Items[0].Depends[0].ID != "orders-db" || len(view.Items[1].Dependents) != 1 || view.Items[1].Dependents[0].ID != "site" {
		t.Fatalf("shared relations=%+v / %+v", view.Items[0].Depends, view.Items[1].Dependents)
	}
}
