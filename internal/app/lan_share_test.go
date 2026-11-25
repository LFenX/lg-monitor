// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
)

// withDeviceBoards offers boards of another device next to the fixture's own, as a signed-in owner would.
type withDeviceBoards struct {
	LANBoards
	device map[string]api.BoardView
}

func (b withDeviceBoards) LANBoardList(ctx context.Context) ([]api.BoardInfo, error) {
	list, err := b.LANBoards.LANBoardList(ctx)
	for _, view := range b.device {
		list = append(list, view.BoardInfo)
	}
	return list, err
}

func (b withDeviceBoards) LANBoardView(ctx context.Context, id string) (api.BoardView, error) {
	if view, ok := b.device[id]; ok {
		return view, nil
	}
	return b.LANBoards.LANBoardView(ctx, id)
}

func TestLANBoardsGivenToExistingViewersNeedNoRequest(t *testing.T) {
	ctx := context.Background()
	port := freeUDPPort(t)
	owner, viewer := newLANFixture(t, "虚构负责人", true, port), newLANFixture(t, "虚构查看者", false, port)
	owner.f.samples(t)
	owner.f.request(t, "POST", "/api/v1/boards", "config", `{"name":"虚构面板"}`, 200)
	owner.f.request(t, "POST", "/api/v1/boards", "config", `{"name":"第三个","id":"third"}`, 200)
	const ops, closed = "cloud~dev-home~ops", "cloud~dev-home~closed"
	synced, err := owner.f.backend.LANBoardView(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	synced.ID, synced.Name = ops, "运维 · 新电脑"
	boards := withDeviceBoards{LANBoards: owner.f.backend, device: map[string]api.BoardView{ops: synced}}
	owner.manager.SetLANBoards(boards)
	first := pairLAN(t, owner, viewer, "main")
	viewers := func() []api.LANViewerItem {
		return lanJSON[[]api.LANViewerItem](t, owner, "GET", "/api/v1/lan/viewers", "read", "", 200)
	}
	people := viewers()
	if len(people) != 1 || people[0].Name != "虚构查看者" || !slices.Equal(people[0].Boards, []string{"main"}) {
		t.Fatalf("viewers=%+v", people)
	}
	grant := func(status int, members []string, ids ...string) api.LANGrantResult {
		t.Helper()
		body, _ := json.Marshal(api.LANGrantInput{BoardIDs: ids, MemberIDs: members})
		response := owner.f.request(t, "POST", "/api/v1/lan/grants", "config", string(body), status)
		if status != 200 {
			return api.LANGrantResult{}
		}
		return decodeAs[api.LANGrantResult](t, response.Body.Bytes())
	}
	granted := func() []string {
		ids := []string{}
		for _, item := range lanJSON[[]api.LANGrantItem](t, owner, "GET", "/api/v1/lan/grants", "read", "", 200) {
			ids = append(ids, item.BoardID)
		}
		slices.Sort(ids)
		return ids
	}
	me := []string{people[0].MemberID}

	// Opening a board to the LAN gives it to nobody, and a choice with anything wrong in it changes nothing.
	owner.f.request(t, "POST", "/api/v1/lan/grants", "read", `{"boardIds":["third"],"memberIds":["`+me[0]+`"]}`, 403)
	grant(404, me, ops, closed)
	grant(409, []string{me[0], "member-stranger"}, ops)
	grant(400, nil, ops)
	if ids := granted(); !slices.Equal(ids, []string{"main"}) {
		t.Fatalf("a refused choice still granted %v", ids)
	}

	if done := grant(200, me, ops); len(done.Granted) != 1 || done.Existing != 0 || done.Granted[0].BoardID != ops || done.Granted[0].BoardName != "运维 · 新电脑" || done.Granted[0].DeviceName != "虚构查看者" || !done.Granted[0].Shared {
		t.Fatalf("grant=%+v", done)
	}
	if again := grant(200, append(me, me[0]), ops, "main", ops); len(again.Granted) != 0 || again.Existing != 2 {
		t.Fatalf("saving the same choice again must add nothing: %+v", again)
	}
	if ids := granted(); !slices.Equal(ids, []string{ops, "main"}) {
		t.Fatalf("granted=%v", ids)
	}

	remote := func() []store.LANOutgoing {
		return lanJSON[[]store.LANOutgoing](t, viewer, "GET", "/api/v1/lan/remote", "read", "", 200)
	}
	find := func(board string) (store.LANOutgoing, bool) {
		list := remote()
		at := slices.IndexFunc(list, func(item store.LANOutgoing) bool { return item.BoardID == board })
		if at < 0 {
			return store.LANOutgoing{}, false
		}
		return list[at], true
	}
	// The viewer's own refresh brings the board in; nothing was requested or paired again.
	appears := func(board string) store.LANOutgoing {
		t.Helper()
		for attempt := 0; attempt < 100; attempt++ {
			owner.clock.Add(2_000)
			viewer.clock.Add(sharedEvery.Milliseconds() + 1)
			if item, ok := find(board); ok {
				return item
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s did not reach the viewer: %+v", board, remote())
		return store.LANOutgoing{}
	}
	pull := func() {
		t.Helper()
		item, err := viewer.f.authDB.LANOutgoingItem(ctx, first.ID)
		if err != nil {
			t.Fatal(err)
		}
		for attempt := 0; attempt < 50; attempt++ {
			owner.clock.Add(2_000)
			if err = viewer.manager.pullShared(ctx, viewer.f.authDB, item); err == nil {
				return
			}
		}
		t.Fatal(err)
	}
	item := appears(ops)
	if item.BoardName != "运维 · 新电脑" || item.OwnerName != "虚构负责人" || item.OwnerAccount == "" || item.Fingerprint != first.Fingerprint || item.Code != "" {
		t.Fatalf("shared board=%+v", item)
	}
	owner.clock.Add(2_000)
	if view := lanJSON[api.LANRemoteBoard](t, viewer, "GET", "/api/v1/lan/remote/"+item.ID, "read", "", 200); len(view.Board.Items) != 1 || view.Board.Items[0].ID != "web" || view.Board.ID != ops {
		t.Fatalf("view=%+v", view)
	}
	if _, ok := find("third"); ok {
		t.Fatal("a board that was not chosen reached the viewer")
	}

	// A board its viewer removed stays away, until they ask for it: they may still view it, so no second approval.
	viewer.f.request(t, "DELETE", "/api/v1/lan/requests/"+item.ID, "config", "", 200)
	pull()
	if _, ok := find(ops); ok {
		t.Fatal("a removed board came back by itself")
	}
	ask := func(board string) store.LANOutgoing {
		owner.clock.Add(13_000)
		body, _ := json.Marshal(api.LANRequestCreate{Address: first.Address, BoardID: board})
		return lanJSON[store.LANOutgoing](t, viewer, "POST", "/api/v1/lan/requests", "config", string(body), 200)
	}
	pending := func() int { return lanJSON[api.LANState](t, owner, "GET", "/api/v1/lan", "read", "", 200).Pending }
	if sent := ask(ops); sent.Status != store.LANApproved || pending() != 0 {
		t.Fatalf("asking again=%+v pending=%d", sent, pending())
	}
	item, _ = find(ops)

	// Revoking works as for a requested board, and giving it again brings the same entry back.
	revoke := func(board string) {
		t.Helper()
		list := lanJSON[[]api.LANGrantItem](t, owner, "GET", "/api/v1/lan/grants", "read", "", 200)
		at := slices.IndexFunc(list, func(grant api.LANGrantItem) bool { return grant.BoardID == board })
		owner.f.request(t, "DELETE", "/api/v1/lan/grants/"+list[at].ID, "config", "", 200)
	}
	revoke(ops)
	owner.clock.Add(2_000)
	viewer.f.request(t, "GET", "/api/v1/lan/remote/"+item.ID, "read", "", 403)
	pull()
	if _, ok := find(ops); ok {
		t.Fatal("a revoked board came back by itself")
	}
	grant(200, me, ops)
	if back := appears(ops); back.ID != item.ID {
		t.Fatalf("giving the board again made a second entry: %+v", back)
	}

	// A request still waiting for the same board is settled along with the grant.
	if sent := ask("third"); sent.Status != store.LANPending || pending() != 1 {
		t.Fatalf("a board without a grant must still be approved: %+v", sent)
	}
	grant(200, me, "third")
	if pending() != 0 {
		t.Fatal("the request for a granted board is still waiting")
	}
	appears("third")

	// A board closed to the LAN is no longer listed, and someone without any board left is no longer offered.
	delete(boards.device, ops)
	owner.clock.Add(2_000)
	viewer.f.request(t, "GET", "/api/v1/lan/remote/"+item.ID, "read", "", 502)
	grant(404, me, ops)
	for _, board := range []string{ops, "third", "main"} {
		revoke(board)
	}
	if people := viewers(); len(people) != 0 {
		t.Fatalf("viewers=%+v", people)
	}
	grant(409, me, "third")

	// The list of given boards needs the identity an owner issued.
	pinned, err := transport.PinnedTLS(owner.authority.CAPEM(), viewer.manager.options.Now)
	if err != nil {
		t.Fatal(err)
	}
	var reply transport.LANShared
	err = viewer.manager.options.Connector.Exchange(ctx, first.Address, "/team/v1/shared", pinned, nil, &reply)
	var remoteErr *transport.TeamRemoteError
	if !errors.As(err, &remoteErr) || remoteErr.Code != "lan_forbidden" {
		t.Fatalf("a stranger read the list: %v %+v", err, reply)
	}
}

// One person who paired twice is offered once and gets one grant, on the identity they use.
func TestLANViewersListOnePersonOnce(t *testing.T) {
	ctx := context.Background()
	owner := newLANFixture(t, "虚构负责人", false, freeUDPPort(t))
	owner.f.request(t, "POST", "/api/v1/boards", "config", `{"name":"虚构面板"}`, 200)
	if err := owner.manager.EnsureLANTeam(ctx); err != nil {
		t.Fatal(err)
	}
	db := owner.f.authDB
	add := func(member, board string) {
		t.Helper()
		if _, err := db.AddTeamMember(ctx, store.TeamMember{ID: member, TeamID: lanTeamID, Name: "虚构设备", Serial: member}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.AddLANGrant(ctx, store.LANGrant{ID: "grant-" + member, MemberID: member, BoardID: board, DeviceName: "虚构查看者"}); err != nil {
			t.Fatal(err)
		}
	}
	add("member-old", "one")
	add("member-new", "two")
	add("member-gone", "one")
	for _, value := range [][2]string{{"member-old", "one"}, {"member-new", "two"}} {
		if _, err := db.UpdateLANGrantViewer(ctx, value[0], value[1], "虚构查看者", "", strings.Repeat("A", 64)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.VisitTeam(ctx, "member-old", "member-old"); err != nil {
		t.Fatal(err)
	}
	if err := db.RevokeTeamMember(ctx, lanTeamID, "member-gone"); err != nil {
		t.Fatal(err)
	}
	people := lanJSON[[]api.LANViewerItem](t, owner, "GET", "/api/v1/lan/viewers", "read", "", 200)
	if len(people) != 1 || people[0].MemberID != "member-old" || len(people[0].Boards) != 2 {
		t.Fatalf("viewers=%+v", people)
	}
	done := lanJSON[api.LANGrantResult](t, owner, "POST", "/api/v1/lan/grants", "config", `{"boardIds":["main"],"memberIds":["member-new","member-old"]}`, 200)
	if len(done.Granted) != 1 || done.Granted[0].MemberID != "member-old" || done.Granted[0].Fingerprint == "" {
		t.Fatalf("grant=%+v", done)
	}
}
