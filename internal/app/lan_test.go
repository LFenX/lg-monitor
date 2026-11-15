package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
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

	request := func(board string) store.LANOutgoing {
		owner.clock.Add(13_000)
		body, _ := json.Marshal(api.LANRequestCreate{Address: peers[0].Address, BoardID: board, Fingerprint: peers[0].Fingerprint})
		return lanJSON[store.LANOutgoing](t, viewer, "POST", "/api/v1/lan/requests", "config", string(body), 200)
	}
	viewer.f.request(t, "POST", "/api/v1/lan/requests", "read", `{"address":"127.0.0.1","boardId":"main"}`, 403)
	sent := request("main")
	if sent.Status != store.LANPending || len(sent.Code) != 7 || sent.BoardName != "虚构面板" || sent.OwnerName != "虚构负责人" {
		t.Fatalf("sent=%+v", sent)
	}
	received := lanJSON[api.LANRequests](t, owner, "GET", "/api/v1/lan/requests", "read", "", 200)
	if len(received.Incoming) != 1 || received.Incoming[0].Code != sent.Code || received.Incoming[0].DeviceName != "虚构查看者" || received.Incoming[0].BoardName != "虚构面板" {
		t.Fatalf("both screens must show the same pairing code: %+v vs %s", received.Incoming, sent.Code)
	}
	viewer.f.request(t, "GET", "/api/v1/lan/remote/"+sent.ID, "read", "", 404)
	owner.f.request(t, "POST", "/api/v1/lan/requests/"+received.Incoming[0].ID+"/approve", "read", "", 403)
	owner.f.request(t, "POST", "/api/v1/lan/requests/"+received.Incoming[0].ID+"/approve", "config", "", 200)
	owner.f.request(t, "POST", "/api/v1/lan/requests/"+received.Incoming[0].ID+"/approve", "config", "", 409)

	owner.clock.Add(2_000)
	mine := lanJSON[api.LANRequests](t, viewer, "GET", "/api/v1/lan/requests", "read", "", 200)
	if len(mine.Outgoing) != 1 || mine.Outgoing[0].Status != store.LANApproved {
		t.Fatalf("outgoing=%+v", mine.Outgoing)
	}
	remote := lanJSON[api.LANRemoteBoard](t, viewer, "GET", "/api/v1/lan/remote/"+sent.ID, "read", "", 200)
	if remote.OwnerName != "虚构负责人" || remote.Board.ID != "main" || len(remote.Board.Items) != 1 || remote.Board.Items[0].ID != "web" {
		t.Fatalf("remote=%+v", remote)
	}
	if item := remote.Board.Items[0]; item.MonitorURL != "" || item.URL != "" || len(item.Rules) != 0 || remote.Board.URL != "" {
		t.Fatalf("links into the owner's own page leaked: %+v", item)
	}
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
	if len(grants) != 1 || grants[0].DeviceName != "虚构查看者" || grants[0].BoardName != "虚构面板" {
		t.Fatalf("grants=%+v", grants)
	}
	owner.f.request(t, "DELETE", "/api/v1/lan/grants/"+grants[0].ID, "config", "", 200)
	owner.clock.Add(2_000)
	if refused := viewer.f.request(t, "GET", "/api/v1/lan/remote/"+sent.ID, "read", "", 502); !strings.Contains(refused.Body.String(), "撤销") {
		t.Fatal(refused.Body.String())
	}

	// Hiding the board list stops the directory; existing data is not exposed to newcomers.
	owner.f.request(t, "PUT", "/api/v1/lan", "config", `{"visible":false}`, 200)
	if hidden := viewer.f.request(t, "GET", "/api/v1/lan/directory?address="+peers[0].Address, "read", "", 502); !strings.Contains(hidden.Body.String(), "内网可见") {
		t.Fatal(hidden.Body.String())
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
