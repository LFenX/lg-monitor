package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
)

type teamTestKeys struct {
	mu     sync.Mutex
	values map[string][]byte
}

type cancelTeamQueries struct {
	TeamQueries
	started  chan struct{}
	canceled chan struct{}
}

func (q *cancelTeamQueries) QueryShare(ctx context.Context, query string, scope store.ShareScope, params map[string]any) (rules.QueryResult, error) {
	if !strings.Contains(query, "in-flight-private-row") {
		return q.TeamQueries.QueryShare(ctx, query, scope, params)
	}
	close(q.started)
	<-ctx.Done()
	close(q.canceled)
	// Even a backend that returns rows after cancellation cannot commit a response.
	return rules.QueryResult{Columns: []string{"value"}, Rows: [][]any{{"in-flight-private-row"}}}, nil
}

func TestTeamRevocationCancelsInFlightRows(t *testing.T) {
	for _, action := range []string{"share", "member"} {
		t.Run(action, func(t *testing.T) {
			owner, member := newTeamFixture(t), newTeamFixture(t)
			loadAPIDashboard(t, owner.f, strings.Replace(teamDashboard, "SELECT collector_id,target_id FROM v_target_current ORDER BY collector_id,target_id", "SELECT 'in-flight-private-row' AS value", 1))
			owner.f.samples(t)
			team := createFixtureTeam(t, owner)
			joined := joinFixtureTeam(t, owner, member, inviteFixtureMember(t, owner, team))
			body, _ := json.Marshal(api.ShareInput{TeamID: team.ID, DashboardID: "overview", Panels: []string{"hosts"}})
			w := owner.f.request(t, "POST", "/api/v1/shares", "admin", string(body), 200)
			var share store.TeamShare
			if err := json.Unmarshal(w.Body.Bytes(), &share); err != nil {
				t.Fatal(err)
			}
			query := &cancelTeamQueries{TeamQueries: owner.manager.options.Queries, started: make(chan struct{}), canceled: make(chan struct{})}
			owner.manager.options.Queries = query
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			joinedSecret, err := member.f.authDB.JoinedTeam(ctx, joined.ID)
			if err != nil {
				t.Fatal(err)
			}
			clientTLS, err := transport.LoadTeamTLS(ctx, member.keys, joinedSecret.CA, joinedSecret.Certificate, joinedSecret.KeyRef, joinedSecret.Fingerprint, member.manager.options.Now)
			if err != nil {
				t.Fatal(err)
			}
			type reply struct {
				Data api.SharedPanelData
				Err  error
			}
			done := make(chan reply, 1)
			go func() {
				var value api.SharedPanelData
				err := (transport.NativeTeamConnector{}).Exchange(ctx, joinedSecret.Address, "/team/v1/shares/"+share.ID+"/panels/hosts", clientTLS, nil, &value)
				done <- reply{value, err}
			}()
			select {
			case <-query.started:
			case <-ctx.Done():
				t.Fatal("query never started")
			}
			path := "/api/v1/shares/" + share.ID
			if action == "member" {
				path = "/api/v1/teams/" + team.ID + "/members/" + joined.MemberID
			}
			owner.f.request(t, "DELETE", path, "admin", "", 200)
			select {
			case <-query.canceled:
			case <-ctx.Done():
				t.Fatal("revocation did not cancel in-flight query")
			}
			select {
			case result := <-done:
				if result.Err == nil || len(result.Data.Rows) != 0 {
					t.Fatal("revoked in-flight rows escaped", result)
				}
			case <-ctx.Done():
				t.Fatal("revoked request stayed open")
			}
		})
	}
}

func (k *teamTestKeys) Get(_ context.Context, name string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.values[name]
	if !ok {
		return nil, fmt.Errorf("fake key missing")
	}
	return append([]byte(nil), v...), nil
}
func (k *teamTestKeys) Set(_ context.Context, name string, value []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.values[name] = append([]byte(nil), value...)
	return nil
}

type teamTestSANs struct{}

func (teamTestSANs) Discover(context.Context) (transport.SANs, error) {
	return transport.SANs{Names: []string{"localhost"}, IPs: []net.IP{net.ParseIP("127.0.0.1")}}, nil
}

type teamFixture struct {
	f         *apiFixture
	manager   *TeamManager
	node      *TeamNode
	keys      *teamTestKeys
	authority *transport.Authority
	clock     atomic.Int64
}

func newTeamFixture(t *testing.T, fixtures ...*apiFixture) *teamFixture {
	t.Helper()
	var f *apiFixture
	if len(fixtures) > 0 {
		f = fixtures[0]
	} else {
		f = newAPIFixture(t)
	}
	return newTeamFixtureWithElapsed(t, f, nil)
}

func newTeamFixtureWithElapsed(t *testing.T, f *apiFixture, elapsed func() time.Duration) *teamFixture {
	t.Helper()
	f.files.MapFS["settings.yaml"] = &fstest.MapFile{Data: []byte("teamListen: 127.0.0.1:0\n")}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	x := &teamFixture{f: f, keys: &teamTestKeys{values: map[string][]byte{}}}
	x.clock.Store(f.now.UnixMilli())
	now := func() time.Time {
		value := time.UnixMilli(x.clock.Load())
		if elapsed != nil {
			value = value.Add(elapsed())
		}
		return value
	}
	authority, err := transport.NewAuthority(context.Background(), transport.AuthorityOptions{Keys: x.keys, State: transport.FileState{Home: t.TempDir()}, SANs: teamTestSANs{}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	x.authority = authority
	x.manager, err = NewTeamManager(TeamOptions{Config: f.manager, Repository: f.authDB, Authority: authority, Queries: f.sqlView, Keys: x.keys, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	f.backend.options.Teams = x.manager
	x.node, err = NewTeamNode(context.Background(), x.manager, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(x.node.Close)
	return x
}

const teamDashboard = `id: overview
title: 虚构共享面板
refresh: 10s
defaultRange: 1h
panels:
- id: hosts
  type: table
  title: 已分享主机
  width: 6
  query: SELECT collector_id,target_id FROM v_target_current ORDER BY collector_id,target_id
- id: count
  type: stat
  title: 已分享目标数
  width: 6
  query: SELECT count(*) AS value FROM target_status
- id: private
  type: table
  title: 未分享的卡片
  width: 12
  query: SELECT 'fake-private-panel' AS value
`

func createFixtureTeam(t *testing.T, x *teamFixture) store.Team {
	t.Helper()
	w := x.f.request(t, "POST", "/api/v1/teams", "admin", `{"name":"虚构团队","ownerName":"虚构负责人"}`, 200)
	var team store.Team
	if err := json.Unmarshal(w.Body.Bytes(), &team); err != nil {
		t.Fatal(err)
	}
	return team
}
func inviteFixtureMember(t *testing.T, x *teamFixture, team store.Team) api.TeamInviteResult {
	t.Helper()
	w := x.f.request(t, "POST", "/api/v1/teams/"+team.ID+"/invites", "admin", `{"name":"虚构成员"}`, 200)
	var value api.TeamInviteResult
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}
func joinFixtureTeam(t *testing.T, owner, member *teamFixture, invite api.TeamInviteResult) store.JoinedTeam {
	t.Helper()
	owner.clock.Add(1100)
	input, _ := json.Marshal(api.TeamJoinInput{Invitation: invite.Invitation, Fingerprint: invite.Fingerprint})
	w := member.f.request(t, "POST", "/api/v1/team-joins", "admin", string(input), 200)
	var value store.JoinedTeam
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestTeamHTTPFixtureLifecycleScopesAndIdentityBoundaries(t *testing.T) {
	owner, member, other := newTeamFixture(t), newTeamFixture(t), newTeamFixture(t)
	ctx := context.Background()
	if result := owner.f.request(t, "GET", "/api/v1/teams/state", "read", "", 200).Body.String(); !strings.Contains(result, `"enabled":false`) {
		t.Fatal("guest enabled team", result)
	}
	owner.node.mu.Lock()
	listening := owner.node.current != nil
	owner.node.mu.Unlock()
	if listening {
		t.Fatal("guest opened team port")
	}
	loadAPIDashboard(t, owner.f, teamDashboard)
	owner.f.samples(t)
	if err := owner.f.db.RegisterCollector(ctx, store.CollectorRecord{ID: "outside-host", Hostname: "outside-secret-host", FirstSeen: 1, LastSeen: owner.f.now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err := owner.f.db.Write(ctx, pipeline.Message{CollectorID: "outside-host", Seq: 1, TS: owner.f.now.UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: "down"}}); err != nil {
		t.Fatal(err)
	}
	if err := owner.f.db.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	team := createFixtureTeam(t, owner)
	invite := inviteFixtureMember(t, owner, team)
	if len(strings.ReplaceAll(invite.Fingerprint, "-", "")) != 16 || invite.Invitation == "" {
		t.Fatal("invitation missing trusted fingerprint")
	}
	preview, _ := json.Marshal(api.TeamJoinInput{Invitation: invite.Invitation})
	member.f.request(t, "POST", "/api/v1/team-joins/preview", "admin", string(preview), 200)
	member.f.request(t, "POST", "/api/v1/team-joins", "admin", string(preview), 400)
	joined := joinFixtureTeam(t, owner, member, invite)
	otherJoined := joinFixtureTeam(t, owner, other, inviteFixtureMember(t, owner, team))
	if joined.MemberID == otherJoined.MemberID {
		t.Fatal("devices reused one identity")
	}
	owner.f.request(t, "POST", "/api/v1/teams", "config", `{"name":"denied"}`, 403)
	member.f.request(t, "POST", "/api/v1/team-joins", "read", string(preview), 403)
	shareInput := api.ShareInput{TeamID: team.ID, DashboardID: "overview", Panels: []string{"hosts", "count"}, Collectors: []string{"fake-host"}, Targets: []string{"web"}, Members: []string{joined.MemberID}}
	body, _ := json.Marshal(shareInput)
	w := owner.f.request(t, "POST", "/api/v1/shares", "admin", string(body), 200)
	var share store.TeamShare
	if err := json.Unmarshal(w.Body.Bytes(), &share); err != nil {
		t.Fatal(err)
	}
	owner.f.request(t, "POST", "/api/v1/shares", "config", string(body), 403)
	for _, field := range []string{"collectors", "targets", "members"} {
		bad := fmt.Sprintf(`{"teamId":%q,"dashboardId":"overview",%q:[]}`, team.ID, field)
		owner.f.request(t, "POST", "/api/v1/shares", "admin", bad, 400)
		owner.f.request(t, "POST", "/api/v1/shares", "admin", strings.Replace(bad, "[]", "null", 1), 400)
	}
	base := "/api/v1/team-joins/" + joined.ID + "/shares"
	definition := member.f.request(t, "GET", base+"/"+share.ID, "read", "", 200).Body.String()
	for _, private := range []string{"outside-secret", "fake-private-panel", "SELECT ", `"query"`, `"file"`, `"certificate"`, `"keyRef"`} {
		if strings.Contains(definition, private) {
			t.Fatalf("private definition field escaped: %s", private)
		}
	}
	if strings.Contains(other.f.request(t, "GET", "/api/v1/team-joins/"+otherJoined.ID+"/shares", "read", "", 200).Body.String(), share.ID) {
		t.Fatal("unselected member saw share")
	}
	other.f.request(t, "GET", "/api/v1/team-joins/"+otherJoined.ID+"/shares/"+share.ID, "read", "", 404)
	data := member.f.request(t, "GET", base+"/"+share.ID+"/panels/hosts", "read", "", 200).Body.String()
	if !strings.Contains(data, "fake-host") || strings.Contains(data, "outside-host") || strings.Contains(data, "SELECT ") {
		t.Fatal("scope escaped", data)
	}
	member.f.request(t, "GET", base+"/"+share.ID+"/panels/hosts", "read", "", 429)
	member.f.request(t, "GET", base+"/"+share.ID+"/panels/private", "read", "", 404)
	member.f.request(t, "GET", base+"/"+share.ID+"/panels/count?collector=outside-host", "read", "", 403)
	member.f.request(t, "GET", base+"/"+share.ID+"/panels/count?sql=SELECT%20secret", "read", "", 400)
	joinedSecret, err := member.f.authDB.JoinedTeam(ctx, joined.ID)
	if err != nil {
		t.Fatal(err)
	}
	clientTLS, err := transport.LoadTeamTLS(ctx, member.keys, joinedSecret.CA, joinedSecret.Certificate, joinedSecret.KeyRef, joinedSecret.Fingerprint, member.manager.options.Now)
	if err != nil {
		t.Fatal(err)
	}
	plain := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: clientTLS.RootCAs, Time: member.manager.options.Now}
	var result any
	if err := (transport.NativeTeamConnector{}).Exchange(ctx, invite.Address, "/team/v1/shares", plain, nil, &result); err == nil {
		t.Fatal("missing member certificate accepted")
	}
	if err := (transport.NativeTeamConnector{}).Exchange(ctx, invite.Address, "/team/v1/shares", owner.authority.AdminTLS(), nil, &result); err == nil {
		t.Fatal("owner admin certificate treated as member")
	}
	request, _ := http.NewRequest("GET", "http://127.0.0.1:7444/api/v1/teams", nil)
	request.RemoteAddr = "192.0.2.5:1234"
	request.TLS = &tls.ConnectionState{}
	request.TLS.PeerCertificates = append(request.TLS.PeerCertificates, clientTLS.Certificates[0].Leaf)
	response := httptest.NewRecorder()
	owner.f.server.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("member certificate became an API bearer credential", response.Code)
	}
	owner.f.request(t, "DELETE", "/api/v1/shares/"+share.ID, "admin", "", 200)
	member.f.request(t, "GET", base+"/"+share.ID, "read", "", 404)
	owner.f.request(t, "DELETE", "/api/v1/teams/"+team.ID+"/members/"+joined.MemberID, "admin", "", 200)
	member.f.request(t, "GET", base, "read", "", 503)
	owner.node.Close()
	other.f.request(t, "GET", "/api/v1/team-joins/"+otherJoined.ID+"/shares", "read", "", 503)
	if len(owner.f.client.sent)+len(member.f.client.sent) != 0 || owner.f.files.writes+member.f.files.writes != 0 {
		t.Fatal("team fixture changed config or sent notification")
	}
}

func TestTeamWholePageMembershipPanelChangesAndAddressKeepIdentity(t *testing.T) {
	owner, member := newTeamFixture(t), newTeamFixture(t)
	loadAPIDashboard(t, owner.f, teamDashboard)
	owner.f.samples(t)
	team := createFixtureTeam(t, owner)
	create := func(panels []string) store.TeamShare {
		t.Helper()
		body, _ := json.Marshal(api.ShareInput{TeamID: team.ID, DashboardID: "overview", Panels: panels})
		w := owner.f.request(t, "POST", "/api/v1/shares", "admin", string(body), 200)
		var share store.TeamShare
		if err := json.Unmarshal(w.Body.Bytes(), &share); err != nil {
			t.Fatal(err)
		}
		return share
	}
	whole, selected := create(nil), create([]string{"hosts"})
	joined := joinFixtureTeam(t, owner, member, inviteFixtureMember(t, owner, team))
	base := "/api/v1/team-joins/" + joined.ID + "/shares/"
	definition := func(id string) api.SharedDashboard {
		t.Helper()
		var value api.SharedDashboard
		w := member.f.request(t, "GET", base+id, "read", "", 200)
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if len(definition(whole.ID).Panels) != 3 || len(definition(selected.ID).Panels) != 1 {
		t.Fatal("later team member missed whole-team share")
	}
	member.f.request(t, "GET", base+whole.ID+"/panels/hosts", "read", "", 200)
	member.f.request(t, "GET", base+selected.ID+"/panels/hosts", "read", "", 429)
	added := teamDashboard + "- id: added\n  type: stat\n  title: 新增卡片\n  width: 6\n  query: SELECT 1 AS value\n"
	loadAPIDashboard(t, owner.f, added)
	if len(definition(whole.ID).Panels) != 4 || len(definition(selected.ID).Panels) != 1 {
		t.Fatal("new panel selection drifted")
	}
	removed := strings.Replace(added, "- id: hosts\n  type: table\n  title: 已分享主机\n  width: 6\n  query: SELECT collector_id,target_id FROM v_target_current ORDER BY collector_id,target_id\n", "", 1)
	loadAPIDashboard(t, owner.f, removed)
	if len(definition(selected.ID).Panels) != 0 {
		t.Fatal("deleted panel stayed shared")
	}
	loadAPIDashboard(t, owner.f, "id: [")
	member.f.request(t, "GET", base+whole.ID, "read", "", 404)
	loadAPIDashboard(t, owner.f, teamDashboard)
	secretBefore, err := member.f.authDB.JoinedTeam(context.Background(), joined.ID)
	if err != nil {
		t.Fatal(err)
	}
	changedAddress := strings.Replace(joined.Address, "127.0.0.1", "localhost", 1)
	body, _ := json.Marshal(api.TeamAddressInput{Address: changedAddress})
	member.f.request(t, "PUT", "/api/v1/team-joins/"+joined.ID, "admin", string(body), 200)
	definition(whole.ID)
	secretAfter, err := member.f.authDB.JoinedTeam(context.Background(), joined.ID)
	if err != nil || secretAfter.Fingerprint != secretBefore.Fingerprint || secretAfter.KeyRef != secretBefore.KeyRef || string(secretAfter.Certificate) != string(secretBefore.Certificate) {
		t.Fatal("address edit rotated identity", err)
	}
	loadAPIDashboard(t, owner.f, "id: overview\ntitle: HTML 面板\nrefresh: 10s\nhtml: overview.html\n")
	member.f.request(t, "GET", base+whole.ID, "read", "", 404)
	owner.f.request(t, "POST", "/api/v1/shares", "admin", `{"teamId":"`+team.ID+`","dashboardId":"overview"}`, 400)
}

func TestTeamListenerRebindFailureKeepsOldEndpointAndSuccessfulBindClosesIt(t *testing.T) {
	owner := newTeamFixture(t)
	team := createFixtureTeam(t, owner)
	invite := inviteFixtureMember(t, owner, team)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	owner.f.files.MapFS["settings.yaml"] = &fstest.MapFile{Data: []byte("teamListen: " + occupied.Addr().String() + "\n")}
	if err := owner.f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wait := func(ready func() bool) {
		t.Helper()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for !ready() {
			select {
			case <-ctx.Done():
				t.Fatal("team listener state did not settle")
			case <-tick.C:
			}
		}
	}
	wait(func() bool { owner.node.mu.Lock(); defer owner.node.mu.Unlock(); return owner.node.lastErr != nil })
	if _, _, err := transport.PreviewTeamInvitation(ctx, transport.TeamInvitation{Address: invite.Address, Fingerprint: strings.ReplaceAll(invite.Fingerprint, "-", "")}, transport.NativeTeamConnector{}, owner.manager.options.Now); err != nil {
		t.Fatal("failed rebind stopped existing listener", err)
	}
	owner.f.files.MapFS["settings.yaml"] = &fstest.MapFile{Data: []byte("teamListen: 127.0.0.1:00\n")}
	if err := owner.f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		owner.node.mu.Lock()
		defer owner.node.mu.Unlock()
		return owner.node.current != nil && owner.node.lastErr == nil && owner.node.current.address != invite.Address
	})
	latest, err := owner.node.publicAddress(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := transport.PreviewTeamInvitation(ctx, transport.TeamInvitation{Address: latest, Fingerprint: strings.ReplaceAll(invite.Fingerprint, "-", "")}, transport.NativeTeamConnector{}, owner.manager.options.Now); err != nil {
		t.Fatal("new listener pin changed", err)
	}
	wait(func() bool {
		u := strings.TrimPrefix(invite.Address, "https://")
		c, err := net.DialTimeout("tcp", u, 50*time.Millisecond)
		if c != nil {
			c.Close()
		}
		return err != nil
	})
}
