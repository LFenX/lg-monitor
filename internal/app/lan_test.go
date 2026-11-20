package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/profile"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
)

// loopbackPackets keeps every discovery socket of a test on 127.0.0.1.
type loopbackPackets struct{}

func (loopbackPackets) ListenPacket(_ context.Context, address string) (net.PacketConn, error) {
	return net.ListenPacket("udp4", strings.Replace(address, "0.0.0.0", "127.0.0.1", 1))
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

func newLANFixture(t *testing.T, name string, visible bool, port int) *teamFixture {
	t.Helper()
	f := newAPIFixture(t)
	settings := "teamListen: 127.0.0.1:0\nlan:\n  name: " + name + "\n"
	if visible {
		settings += "  visible: true\n"
	}
	f.files.MapFS["settings.yaml"] = &fstest.MapFile{Data: []byte(settings)}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	x := &teamFixture{f: f, keys: &teamTestKeys{values: map[string][]byte{}}}
	x.clock.Store(f.now.UnixMilli())
	now := func() time.Time { return time.UnixMilli(x.clock.Load()) }
	authority, err := transport.NewAuthority(context.Background(), transport.AuthorityOptions{Keys: x.keys, State: transport.FileState{Home: t.TempDir()}, SANs: teamTestSANs{}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	x.authority = authority
	x.manager, err = NewTeamManager(TeamOptions{Config: f.manager, Repository: f.authDB, Authority: authority, Queries: f.sqlView, Keys: x.keys, Now: now, Hostname: name + "-host",
		Packets: loopbackPackets{}, DiscoveryPort: port, DiscoveryTargets: func(port int) []net.Addr {
			return []net.Addr{&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}}
		}})
	if err != nil {
		t.Fatal(err)
	}
	x.manager.SetLANBoards(f.backend)
	x.manager.SetLANAssistant(f.backend)
	f.backend.options.Teams = x.manager
	x.node, err = NewTeamNode(context.Background(), x.manager, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(x.node.Close)
	return x
}

func lanJSON[T any](t *testing.T, x *teamFixture, method, path, scope, body string, status int) T {
	t.Helper()
	return decodeAs[T](t, x.f.request(t, method, path, scope, body, status).Body.Bytes())
}

func TestLANRequestApproveViewAndRevoke(t *testing.T) {
	port := freeUDPPort(t)
	owner, viewer := newLANFixture(t, "虚构负责人", true, port), newLANFixture(t, "虚构查看者", false, port)
	owner.f.samples(t)
	owner.f.request(t, "POST", "/api/v1/boards", "config", `{"name":"虚构面板","description":"公开的简介"}`, 200)
	owner.f.request(t, "POST", "/api/v1/boards", "config", `{"name":"第二个","id":"second"}`, 200)
	var state api.LANState
	for attempt := 0; attempt < 100 && !(state.Listening && state.Problem == ""); attempt++ {
		state = lanJSON[api.LANState](t, owner, "GET", "/api/v1/lan", "read", "", 200)
		time.Sleep(20 * time.Millisecond)
	}
	if !state.Visible || !state.Listening || state.Name != "虚构负责人" {
		t.Fatalf("owner state=%+v", state)
	}
	// Discovery finds the owner and its public directory: names and descriptions, no states.
	var peers []api.LANPeerResult
	for attempt := 0; attempt < 20 && len(peers) == 0; attempt++ {
		peers = lanJSON[[]api.LANPeerResult](t, viewer, "POST", "/api/v1/lan/discover", "read", "", 200)
	}
	if len(peers) != 1 || peers[0].Name != "虚构负责人" || peers[0].Directory == nil || len(peers[0].Directory.Boards) != 2 {
		t.Fatalf("peers=%+v", peers)
	}
	directory := peers[0].Directory
	if directory.Boards[0].ID != "main" || directory.Boards[0].Description != "公开的简介" || directory.Boards[0].Services != 1 {
		t.Fatalf("directory=%+v", directory)
	}
	encoded, _ := json.Marshal(directory)
	if strings.Contains(string(encoded), "state") || strings.Contains(string(encoded), "fake.exe") {
		t.Fatalf("the public directory must not carry health or match details: %s", encoded)
	}
	// Each computer has an account from the start; others see its nickname and avatar, not its name.
	ownerProfile := lanJSON[store.Profile](t, owner, "GET", "/api/v1/profile", "read", "", 200)
	viewerProfile := lanJSON[store.Profile](t, viewer, "GET", "/api/v1/profile", "read", "", 200)
	if directory.Avatar == "" || directory.Avatar != ownerProfile.Avatar || directory.Hostname != "" || strings.Contains(string(encoded), "-host") {
		t.Fatalf("directory must show the account, not the computer: %s (profile %+v)", encoded, ownerProfile)
	}

	request := func(board string) store.LANOutgoing {
		owner.clock.Add(13_000)
		body, _ := json.Marshal(api.LANRequestCreate{Address: peers[0].Address, BoardID: board, Fingerprint: peers[0].Fingerprint})
		return lanJSON[store.LANOutgoing](t, viewer, "POST", "/api/v1/lan/requests", "config", string(body), 200)
	}
	viewer.f.request(t, "POST", "/api/v1/lan/requests", "read", `{"address":"127.0.0.1","boardId":"main"}`, 403)
	sent := request("main")
	if sent.Status != store.LANPending || len(sent.Code) != 7 || sent.BoardName != "虚构面板" || sent.OwnerName != "虚构负责人" || sent.OwnerAvatar != ownerProfile.Avatar {
		t.Fatalf("sent=%+v", sent)
	}
	received := lanJSON[api.LANRequests](t, owner, "GET", "/api/v1/lan/requests", "read", "", 200)
	if len(received.Incoming) != 1 || received.Incoming[0].Code != sent.Code || received.Incoming[0].DeviceName != "虚构查看者" || received.Incoming[0].BoardName != "虚构面板" || received.Incoming[0].Avatar != viewerProfile.Avatar || received.Incoming[0].Hostname != "" {
		t.Fatalf("both screens must show the same pairing code: %+v vs %s", received.Incoming, sent.Code)
	}
	viewer.f.request(t, "GET", "/api/v1/lan/remote/"+sent.ID, "read", "", 404)
	owner.f.request(t, "POST", "/api/v1/lan/requests/"+received.Incoming[0].ID+"/approve", "read", "", 403)
	owner.f.request(t, "POST", "/api/v1/lan/requests/"+received.Incoming[0].ID+"/approve", "config", "", 200)
	owner.f.request(t, "POST", "/api/v1/lan/requests/"+received.Incoming[0].ID+"/approve", "config", "", 409)
	// Older viewers can be displayed using a uniquely matched shared-board address.
	back := store.LANOutgoing{ID: "lanout-back", RemoteID: "r", Address: "127.0.0.1:7445", OwnerName: "同一个人", OwnerAvatar: "fiora-6", Fingerprint: strings.Repeat("B", 64), BoardID: "theirs", BoardName: "对方的", Code: "000 000", Status: store.LANApproved, CA: []byte("x"), Secret: "s"}
	if _, err := owner.f.authDB.AddLANOutgoing(context.Background(), back); err != nil {
		t.Fatal(err)
	}
	if early := lanJSON[[]api.LANGrantItem](t, owner, "GET", "/api/v1/lan/grants", "read", "", 200); len(early) != 1 || early[0].DeviceName != "同一个人" || early[0].Avatar != "fiora-6" || early[0].Fingerprint != strings.Repeat("B", 64) {
		t.Fatalf("an older viewer was not recognised by its address: %+v", early)
	}
	back.ID, back.Address, back.Fingerprint = "lanout-other", "127.0.0.1:7446", strings.Repeat("C", 64)
	if _, err := owner.f.authDB.AddLANOutgoing(context.Background(), back); err != nil {
		t.Fatal(err)
	}
	if early := lanJSON[[]api.LANGrantItem](t, owner, "GET", "/api/v1/lan/grants", "read", "", 200); len(early) != 1 || early[0].DeviceName != viewerProfile.Nickname || early[0].Fingerprint != "" {
		t.Fatalf("the fallback matched different peers at the same address: %+v", early)
	}
	if err := owner.f.authDB.DeleteLANOutgoing(context.Background(), back.ID); err != nil {
		t.Fatal(err)
	}

	owner.clock.Add(2_000)
	mine := lanJSON[api.LANRequests](t, viewer, "GET", "/api/v1/lan/requests", "read", "", 200)
	if len(mine.Outgoing) != 1 || mine.Outgoing[0].Status != store.LANApproved {
		t.Fatalf("outgoing=%+v", mine.Outgoing)
	}
	remote := lanJSON[api.LANRemoteBoard](t, viewer, "GET", "/api/v1/lan/remote/"+sent.ID, "read", "", 200)
	if remote.OwnerName != "虚构负责人" || remote.OwnerAvatar != ownerProfile.Avatar || remote.Board.ID != "main" || len(remote.Board.Items) != 1 || remote.Board.Items[0].ID != "web" {
		t.Fatalf("remote=%+v", remote)
	}
	// Looking up the owner's directory refreshes saved requests even before the board is opened again.
	known := lanJSON[[]store.LANOutgoing](t, viewer, "GET", "/api/v1/lan/remote", "read", "", 200)[0]
	renamed := lanJSON[store.Profile](t, owner, "PUT", "/api/v1/profile", "config", `{"nickname":"改过的负责人","shuffle":true}`, 200)
	if renamed.Nickname != "改过的负责人" || renamed.Avatar == ownerProfile.Avatar {
		t.Fatalf("renamed=%+v", renamed)
	}
	lanJSON[transport.LANDirectory](t, viewer, "GET", "/api/v1/lan/directory?address="+peers[0].Address, "read", "", 200)
	if listed := lanJSON[[]store.LANOutgoing](t, viewer, "GET", "/api/v1/lan/remote", "read", "", 200); len(listed) != 1 || listed[0].OwnerName != renamed.Nickname || listed[0].OwnerAvatar != renamed.Avatar || listed[0].OwnerAccount != known.OwnerAccount {
		t.Fatalf("directory did not refresh the saved owner profile: %+v", listed)
	}
	owner.clock.Add(2_000)
	if remote = lanJSON[api.LANRemoteBoard](t, viewer, "GET", "/api/v1/lan/remote/"+sent.ID, "read", "", 200); remote.OwnerName != "改过的负责人" || remote.OwnerAvatar != renamed.Avatar {
		t.Fatalf("remote after rename=%+v", remote)
	}
	if listed := lanJSON[[]store.LANOutgoing](t, viewer, "GET", "/api/v1/lan/remote", "read", "", 200); len(listed) != 1 || listed[0].OwnerName != "改过的负责人" || listed[0].OwnerAvatar != renamed.Avatar {
		t.Fatalf("remote list after rename=%+v", listed)
	}
	if item := remote.Board.Items[0]; item.MonitorURL != "" || item.URL != "" || remote.Board.URL != "" || remote.FetchedAt == 0 {
		t.Fatalf("links into the owner's own page leaked: %+v", item)
	}
	// The viewer opens a shared service as its owner sees it, read-only, and nothing outside the board.
	owner.clock.Add(1_000)
	detail := lanJSON[api.MonitorDetail](t, viewer, "GET", "/api/v1/lan/remote/"+sent.ID+"/monitors/web", "read", "", 200)
	if detail.Monitor.ID != "web" || len(detail.Boards) != 1 || detail.Boards[0].ID != "main" || detail.Monitor.MonitorURL != "" || len(detail.Slots) == 0 {
		t.Fatalf("remote detail=%+v", detail)
	}
	if doc := lanJSON[api.MonitorDoc](t, viewer, "GET", "/api/v1/lan/remote/"+sent.ID+"/monitors/web/doc", "read", "", 200); doc.ID != "web" {
		t.Fatalf("remote doc=%+v", doc)
	}
	owner.clock.Add(1_000)
	viewer.f.request(t, "GET", "/api/v1/lan/remote/"+sent.ID+"/monitors/not-shared", "read", "", 404)
	viewer.f.request(t, "GET", "/api/v1/lan/remote/"+sent.ID+"/monitors/..%2Fsecond", "read", "", 400)
	// The other board is not covered by this approval.
	viewer.f.request(t, "GET", "/api/v1/lan/remote/unknown", "read", "", 404)

	// A second request reuses the device identity, so the owner sees the same member.
	second := request("second")
	received = lanJSON[api.LANRequests](t, owner, "GET", "/api/v1/lan/requests", "read", "", 200)
	pending := received.Incoming[slices.IndexFunc(received.Incoming, func(item api.LANIncoming) bool { return item.Status == store.LANPending })]
	if pending.MemberID != received.Incoming[slices.IndexFunc(received.Incoming, func(item api.LANIncoming) bool { return item.Status == store.LANApproved })].MemberID || pending.Code != second.Code || pending.BoardID != "second" {
		t.Fatalf("a known device must ask with its identity: %+v", received.Incoming)
	}
	owner.f.request(t, "POST", "/api/v1/lan/requests/"+pending.ID+"/deny", "config", "", 200)
	owner.clock.Add(2_000)
	mine = lanJSON[api.LANRequests](t, viewer, "GET", "/api/v1/lan/requests", "read", "", 200)
	if denied := mine.Outgoing[slices.IndexFunc(mine.Outgoing, func(item store.LANOutgoing) bool { return item.BoardID == "second" })]; denied.Status != store.LANDenied {
		t.Fatalf("denied request=%+v", denied)
	}
	if listed := lanJSON[[]store.LANOutgoing](t, viewer, "GET", "/api/v1/lan/remote", "read", "", 200); len(listed) != 1 || listed[0].BoardID != "main" {
		t.Fatalf("remote list=%+v", listed)
	}

	// Revoking takes effect on the next read.
	grants := lanJSON[[]api.LANGrantItem](t, owner, "GET", "/api/v1/lan/grants", "read", "", 200)
	if len(grants) != 1 || grants[0].DeviceName != "虚构查看者" || grants[0].Avatar != viewerProfile.Avatar || grants[0].BoardName != "虚构面板" {
		t.Fatalf("grants=%+v", grants)
	}
	// The viewer's new nickname and avatar reach the owner's list of who can see the board with its next read,
	// so the same person looks the same in every list.
	changed := lanJSON[store.Profile](t, viewer, "PUT", "/api/v1/profile", "config", `{"nickname":"改过的查看者","shuffle":true}`, 200)
	owner.clock.Add(2_000)
	lanJSON[api.LANRemoteBoard](t, viewer, "GET", "/api/v1/lan/remote/"+sent.ID, "read", "", 200)
	grants = lanJSON[[]api.LANGrantItem](t, owner, "GET", "/api/v1/lan/grants", "read", "", 200)
	if len(grants) != 1 || grants[0].DeviceName != "改过的查看者" || grants[0].Avatar != changed.Avatar || grants[0].Fingerprint == "" {
		t.Fatalf("grants after the viewer changed=%+v", grants)
	}
	owner.f.request(t, "DELETE", "/api/v1/lan/grants/"+grants[0].ID, "config", "", 200)
	owner.clock.Add(2_000)
	if refused := viewer.f.request(t, "GET", "/api/v1/lan/remote/"+sent.ID, "read", "", 403); !strings.Contains(refused.Body.String(), "撤销") {
		t.Fatal(refused.Body.String())
	}
	// The viewer forgets a revoked board: it leaves the remote list and later reads fail without contacting the owner.
	if listed := lanJSON[[]store.LANOutgoing](t, viewer, "GET", "/api/v1/lan/remote", "read", "", 200); len(listed) != 0 {
		t.Fatalf("revoked board still listed: %+v", listed)
	}
	if mine := lanJSON[api.LANRequests](t, viewer, "GET", "/api/v1/lan/requests", "read", "", 200); !slices.ContainsFunc(mine.Outgoing, func(item store.LANOutgoing) bool {
		return item.ID == sent.ID && item.Status == store.LANRevoked
	}) {
		t.Fatalf("outgoing=%+v", mine.Outgoing)
	}
	viewer.f.request(t, "GET", "/api/v1/lan/remote/"+sent.ID, "read", "", 404)

	// Hiding the board list stops the directory; existing data is not exposed to newcomers.
	owner.f.request(t, "PUT", "/api/v1/lan", "config", `{"visible":false}`, 200)
	if hidden := viewer.f.request(t, "GET", "/api/v1/lan/directory?address="+peers[0].Address, "read", "", 502); !strings.Contains(hidden.Body.String(), "内网可见") {
		t.Fatal(hidden.Body.String())
	}
}

func TestProfileAPI(t *testing.T) {
	x := newLANFixture(t, "虚构旧名字", false, freeUDPPort(t))
	created := lanJSON[store.Profile](t, x, "GET", "/api/v1/profile", "read", "", 200)
	// An older version's LAN name seeds the nickname; the avatar is one of the bundled ones.
	if created.Nickname != "虚构旧名字" || profile.CheckAvatar(created.Avatar) != nil || created.CreatedAt == 0 {
		t.Fatalf("created=%+v", created)
	}
	x.f.request(t, "PUT", "/api/v1/profile", "read", `{"nickname":"偷改"}`, 403)
	for _, body := range []string{`{"nickname":"  "}`, `{"avatar":"https://example.test/a.png"}`, `{"avatar":"fiora-1","shuffle":true}`, `{"avatar":"data:image/png;base64,PHN2Zz4="}`, `{"unknown":1}`} {
		x.f.request(t, "PUT", "/api/v1/profile", "config", body, 400)
	}
	picture := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nfake"))
	saved := lanJSON[store.Profile](t, x, "PUT", "/api/v1/profile", "config", `{"avatar":"`+picture+`"}`, 200)
	if saved.Avatar != picture || saved.Nickname != "虚构旧名字" {
		t.Fatalf("saved=%+v", saved)
	}
	// Renaming on the LAN page renames the account.
	state := lanJSON[api.LANState](t, x, "PUT", "/api/v1/lan", "config", `{"name":"小李"}`, 200)
	if state.Name != "小李" || state.Avatar != picture || state.Visible {
		t.Fatalf("state=%+v", state)
	}
	if got := lanJSON[store.Profile](t, x, "GET", "/api/v1/profile", "read", "", 200); got.Nickname != "小李" {
		t.Fatalf("profile after LAN rename=%+v", got)
	}
}

func TestLANPairingCodeBindsAuthorityKeyAndRequest(t *testing.T) {
	base := transport.LANCode("AAAA", []byte("fake-public-key"), "lanreq-1")
	for _, other := range []string{
		transport.LANCode("BBBB", []byte("fake-public-key"), "lanreq-1"),
		transport.LANCode("AAAA", []byte("other-public-key"), "lanreq-1"),
		transport.LANCode("AAAA", []byte("fake-public-key"), "lanreq-2"),
	} {
		if other == base {
			t.Fatal("pairing code does not change with the authority, key or request")
		}
	}
	if len(base) != 7 || base[3] != ' ' || transport.LANCode("aaaa", []byte("fake-public-key"), "lanreq-1") != base {
		t.Fatalf("code=%q", base)
	}
}

func TestLANAddressAcceptsWhatPeopleType(t *testing.T) {
	for input, want := range map[string]string{
		"192.168.1.20":                 "https://192.168.1.20:7445",
		"192.168.1.20:7500":            "https://192.168.1.20:7500",
		" https://192.168.1.20:7445/ ": "https://192.168.1.20:7445",
	} {
		if got, err := normalizeLANAddress(input); err != nil || got != want {
			t.Fatalf("%q: %q %v", input, got, err)
		}
	}
	for _, bad := range []string{"", "http://192.168.1.20", "https://192.168.1.20/path"} {
		if _, err := normalizeLANAddress(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// exerciseMCPLAN calls every LAN tool once; discovery stays on loopback and nobody answers.
func exerciseMCPLAN(t *testing.T, f *apiFixture, call, fail func(string, any) string) {
	t.Helper()
	teams := f.backend.options.Teams
	teams.options.Packets = loopbackPackets{}
	teams.options.DiscoveryPort = freeUDPPort(t)
	teams.options.DiscoveryTargets = func(port int) []net.Addr {
		return []net.Addr{&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}}
	}
	if status := call("lan_status", map[string]any{}); !strings.Contains(status, `"fingerprint"`) {
		t.Fatalf("lan_status=%s", status)
	}
	call("lan_configure", map[string]any{"visible": false, "name": "MCP 虚构电脑"})
	if peers := call("lan_discover", map[string]any{}); peers != "[]" {
		t.Fatalf("lan_discover=%s", peers)
	}
	if message := fail("lan_request", map[string]any{"address": "127.0.0.1:1", "boardId": "main"}); !strings.Contains(message, "连不上") {
		t.Fatalf("lan_request=%s", message)
	}
	call("lan_requests", map[string]any{})
	fail("lan_approve", map[string]any{"id": "lanreq-missing"})
	fail("lan_deny", map[string]any{"id": "lanreq-missing"})
	call("lan_grants", map[string]any{})
	fail("lan_revoke", map[string]any{"id": "grant-missing"})
	call("lan_remote", map[string]any{})
	fail("lan_remote_board", map[string]any{"id": "lanout-missing"})
	fail("lan_remote_monitor", map[string]any{"id": "lanout-missing", "monitor": "web"})
	fail("lan_remote_doc", map[string]any{"id": "lanout-missing", "monitor": "web"})
	if departments := call("list_departments", map[string]any{}); !strings.Contains(departments, `"me"`) {
		t.Fatalf("list_departments=%s", departments)
	}
	created := call("create_department", map[string]any{"name": "MCP 虚构部门"})
	var department struct{ ID string }
	_ = json.Unmarshal([]byte(created), &department)
	if status := call("department_action", map[string]any{"host": "local", "id": department.ID, "action": "status"}); !strings.Contains(status, "leader") {
		t.Fatalf("department_action=%s", status)
	}
	call("delete_department", map[string]any{"id": department.ID})
	if account := call("profile", map[string]any{}); !strings.Contains(account, `"MCP 虚构电脑"`) {
		t.Fatalf("profile=%s", account)
	}
	if changed := call("profile_update", map[string]any{"nickname": "MCP 改名", "shuffle": true}); !strings.Contains(changed, `"MCP 改名"`) {
		t.Fatalf("profile_update=%s", changed)
	}
}

// pairLAN has the viewer ask for a board of the owner and the owner approve it, as two people would.
func pairLAN(t *testing.T, owner, viewer *teamFixture, board string) store.LANOutgoing {
	t.Helper()
	var state api.LANState
	for attempt := 0; attempt < 100 && !(state.Listening && state.Problem == ""); attempt++ {
		state = lanJSON[api.LANState](t, owner, "GET", "/api/v1/lan", "read", "", 200)
		time.Sleep(20 * time.Millisecond)
	}
	var peers []api.LANPeerResult
	for attempt := 0; attempt < 20 && len(peers) == 0; attempt++ {
		peers = lanJSON[[]api.LANPeerResult](t, viewer, "POST", "/api/v1/lan/discover", "read", "", 200)
	}
	if len(peers) != 1 {
		t.Fatalf("peers=%+v", peers)
	}
	owner.clock.Add(13_000)
	body, _ := json.Marshal(api.LANRequestCreate{Address: peers[0].Address, BoardID: board, Fingerprint: peers[0].Fingerprint})
	sent := lanJSON[store.LANOutgoing](t, viewer, "POST", "/api/v1/lan/requests", "config", string(body), 200)
	received := lanJSON[api.LANRequests](t, owner, "GET", "/api/v1/lan/requests", "read", "", 200)
	owner.f.request(t, "POST", "/api/v1/lan/requests/"+received.Incoming[0].ID+"/approve", "config", "", 200)
	owner.clock.Add(2_000)
	lanJSON[api.LANRequests](t, viewer, "GET", "/api/v1/lan/requests", "read", "", 200)
	return sent
}

func TestLANModelShareLetsAPairedColleagueUseTheOwnersKey(t *testing.T) {
	port := freeUDPPort(t)
	owner, viewer := newLANFixture(t, "虚构负责人", true, port), newLANFixture(t, "虚构查看者", false, port)
	owner.f.samples(t)
	owner.f.request(t, "POST", "/api/v1/boards", "config", `{"name":"虚构面板"}`, 200)
	pairLAN(t, owner, viewer, "main")
	chat := &fakeDeepSeek{calls: `{"choices":[{"delta":{"content":"同事的模型在回答。"},"finish_reason":"stop"}]}`}
	deepseek := httptest.NewServer(chat)
	t.Cleanup(deepseek.Close)
	owner.f.backend.options.DeepSeekURL = deepseek.URL
	owner.f.vault.values[config.DeepSeekKeyRef] = []byte("FAKE_DEEPSEEK_KEY")
	if status := lanJSON[api.AssistantStatus](t, viewer, "GET", "/api/v1/assistant", "read", "", 200); status.Configured || status.SharedBy != nil {
		t.Fatalf("before sharing: %+v", status)
	}
	shares := lanJSON[[]api.ModelShareItem](t, owner, "GET", "/api/v1/assistant/shares", "read", "", 200)
	if len(shares) != 1 || shares[0].Name != "虚构查看者" || shares[0].Shared {
		t.Fatalf("shares=%+v", shares)
	}
	owner.f.request(t, "PUT", "/api/v1/assistant/shares/"+shares[0].MemberID, "read", `{"shared":true}`, 403)
	owner.f.request(t, "PUT", "/api/v1/assistant/shares/nobody", "config", `{"shared":true}`, 404)
	if item := lanJSON[api.ModelShareItem](t, owner, "PUT", "/api/v1/assistant/shares/"+shares[0].MemberID, "config", `{"shared":true}`, 200); !item.Shared {
		t.Fatalf("share=%+v", item)
	}
	viewer.clock.Add(61_000)
	status := lanJSON[api.AssistantStatus](t, viewer, "GET", "/api/v1/assistant", "read", "", 200)
	if !status.Configured || status.SharedBy == nil || status.SharedBy.Name != "虚构负责人" || status.SharedBy.Model == "" || status.DeepSeekKey {
		t.Fatalf("shared status: %+v", status)
	}
	// The colleague talks to 波西米亚 with no key of their own; the owner's lgmon adds the key on the way out.
	ask := httptest.NewRequest("POST", "http://127.0.0.1:7444/api/v1/assistant/chat", strings.NewReader(`{"messages":[{"role":"user","text":"你好"}]}`))
	ask.RemoteAddr = "192.0.2.5:1234"
	ask.Header.Set("Authorization", "Bearer fake-read")
	recorder := httptest.NewRecorder()
	viewer.f.server.ServeHTTP(recorder, ask)
	answer := recorder.Body.String()
	if !strings.Contains(answer, "同事的模型在回答") {
		t.Fatal(answer)
	}
	chat.mu.Lock()
	keys := slices.Clone(chat.keys)
	chat.mu.Unlock()
	if len(keys) == 0 || slices.ContainsFunc(keys, func(key string) bool { return key != "Bearer FAKE_DEEPSEEK_KEY" }) || viewer.f.vault.values[config.DeepSeekKeyRef] != nil {
		t.Fatalf("keys seen by DeepSeek: %v", keys)
	}
	// Taking the share back ends it with the next look.
	lanJSON[api.ModelShareItem](t, owner, "PUT", "/api/v1/assistant/shares/"+shares[0].MemberID, "config", `{"shared":false}`, 200)
	viewer.clock.Add(61_000)
	if status := lanJSON[api.AssistantStatus](t, viewer, "GET", "/api/v1/assistant", "read", "", 200); status.Configured || status.SharedBy != nil {
		t.Fatalf("after taking it back: %+v", status)
	}
}

func TestDepartmentsAreSeenWithoutSharingBoardsAndLeadersApprove(t *testing.T) {
	port := freeUDPPort(t)
	host, colleague := newLANFixture(t, "虚构负责人", false, port), newLANFixture(t, "虚构同事", false, port)
	created := lanJSON[store.Department](t, host, "POST", "/api/v1/departments", "config", `{"name":"虚构研发部"}`, 200)
	if created.ID == "" || profile.CheckAvatar(created.Avatar) != nil {
		t.Fatalf("created=%+v", created)
	}
	host.f.request(t, "POST", "/api/v1/departments", "read", `{"name":"偷建"}`, 403)
	if !host.f.manager.Snapshot().Settings.LAN.Departments || host.f.manager.Snapshot().Settings.LAN.Visible {
		t.Fatal("hosting a department must answer on the LAN without making boards visible")
	}
	hostID := host.manager.hostID()
	child := lanJSON[store.Department](t, host, "POST", "/api/v1/departments", "config", `{"name":"虚构前端组","parent":"`+hostID+`:`+created.ID+`"}`, 200)
	// The colleague finds the departments by discovery, though the host shares no boards.
	var list api.DepartmentList
	for attempt := 0; attempt < 100; attempt++ {
		list = lanJSON[api.DepartmentList](t, colleague, "GET", "/api/v1/departments", "read", "", 200)
		if len(list.Hosts) == 1 {
			break
		}
		colleague.manager.dept.mu.Lock()
		colleague.manager.dept.found = time.Time{}
		colleague.manager.dept.mu.Unlock()
		time.Sleep(30 * time.Millisecond)
	}
	if len(list.Hosts) != 1 || list.Hosts[0].Name != "虚构负责人" || len(list.Hosts[0].Departments) != 2 || list.Hosts[0].Departments[1].Parent != hostID+":"+created.ID {
		t.Fatalf("departments seen: %+v", list)
	}
	if peers := lanJSON[[]api.LANPeerResult](t, colleague, "POST", "/api/v1/lan/discover", "read", "", 200); len(peers) != 0 {
		t.Fatalf("a computer hosting only departments showed up for boards: %+v", peers)
	}
	act := func(x *teamFixture, hostID, id, action, body string, status int) map[string]any {
		host.clock.Add(1_000)
		return lanJSON[map[string]any](t, x, "POST", "/api/v1/departments/"+hostID+"/"+id+"/"+action, "config", body, status)
	}
	if joined := act(colleague, hostID, created.ID, "join", `{}`, 200); joined["role"] != "pending" {
		t.Fatalf("join=%v", joined)
	}
	if list := lanJSON[api.DepartmentList](t, colleague, "GET", "/api/v1/departments", "read", "", 200); list.Pending[hostID+":"+created.ID] != "pending" {
		t.Fatalf("pending=%v", list.Pending)
	}
	// Only a leader sees and decides requests; the colleague is not one yet.
	act(colleague, hostID, created.ID, "requests", `{}`, 403)
	requests := lanJSON[struct{ Requests []store.DepartmentRequest }](t, host, "POST", "/api/v1/departments/local/"+created.ID+"/requests", "config", `{}`, 200).Requests
	if len(requests) != 1 || requests[0].Name != "虚构同事" {
		t.Fatalf("requests=%+v", requests)
	}
	act(host, "local", created.ID, "decide", `{"request":"`+requests[0].ID+`","approve":true}`, 200)
	me := lanJSON[api.DepartmentList](t, colleague, "GET", "/api/v1/departments", "read", "", 200).Me
	if status := act(colleague, hostID, created.ID, "status", `{}`, 200); status["role"] != "member" {
		t.Fatalf("status after approval=%v", status)
	}
	// Made a leader, the colleague approves the next request from their own computer.
	act(host, "local", created.ID, "role", `{"key":"`+me+`","role":"leader"}`, 200)
	act(colleague, hostID, created.ID, "requests", `{}`, 200)
	act(colleague, hostID, created.ID, "edit", `{"name":"虚构研发中心"}`, 200)
	if department, _ := host.f.authDB.Department(context.Background(), created.ID); department.Name != "虚构研发中心" {
		t.Fatalf("edited=%+v", department)
	}
	// A rename keeps a department where it is; moving it is its own field, and never under itself or its own branch.
	act(host, "local", child.ID, "edit", `{"name":"虚构前端小组","avatar":"fiora-7"}`, 200)
	if department, _ := host.f.authDB.Department(context.Background(), child.ID); department.Parent != hostID+":"+created.ID || department.Avatar != "fiora-7" {
		t.Fatalf("renamed child=%+v", department)
	}
	act(host, "local", created.ID, "edit", `{"parent":"`+hostID+`:`+child.ID+`"}`, 400)
	act(host, "local", created.ID, "edit", `{"parent":"`+hostID+`:`+created.ID+`"}`, 400)
	act(host, "local", created.ID, "edit", `{"parent":"no-colon"}`, 400)
	act(host, "local", child.ID, "edit", `{"parent":""}`, 200)
	if department, _ := host.f.authDB.Department(context.Background(), child.ID); department.Parent != "" {
		t.Fatalf("moved to the top=%+v", department)
	}
	act(host, "local", child.ID, "edit", `{"parent":"`+hostID+`:`+created.ID+`"}`, 200)
	if department, _ := host.f.authDB.Department(context.Background(), child.ID); department.Parent != hostID+":"+created.ID {
		t.Fatalf("moved back=%+v", department)
	}
	// A request signed by one computer cannot be replayed as another's.
	body, _ := json.Marshal(transport.SignedRequest{Account: me, Time: host.manager.options.Now().UnixMilli(), Signature: "AAAA", Data: json.RawMessage(`{}`)})
	if _, _, err := host.manager.verifySigned("/team/v1/departments/"+created.ID+"/requests", body); err == nil {
		t.Fatal("a forged signature was accepted")
	}
	// The last leader cannot leave a department with people in it, and deleting a parent lifts its children.
	act(host, "local", created.ID, "role", `{"key":"`+me+`","role":"member"}`, 200)
	act(host, "local", created.ID, "leave", `{}`, 400)
	host.f.request(t, "DELETE", "/api/v1/departments/"+created.ID, "config", "", 200)
	if department, _ := host.f.authDB.Department(context.Background(), child.ID); department.Parent != "" {
		t.Fatalf("child after deleting its parent=%+v", department)
	}
	host.f.request(t, "DELETE", "/api/v1/departments/"+child.ID, "config", "", 200)
	if host.f.manager.Snapshot().Settings.LAN.Departments {
		t.Fatal("with no departments left, the computer must stop answering for them")
	}
}
