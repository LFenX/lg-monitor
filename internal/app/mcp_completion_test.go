package app

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/store"
)

func exerciseMCPTeams(t *testing.T, f *apiFixture, proxy bool, called map[string]bool, call func(string, any) string) {
	t.Helper()
	owner, member := newTeamFixture(t, f), newTeamFixture(t)
	loadAPIDashboard(t, f, teamDashboard)
	memberHTTP := httptest.NewServer(member.f.server)
	t.Cleanup(memberHTTP.Close)
	memberSession := mcpSession(t, memberHTTP.URL, "fake-admin", proxy)
	readSession := mcpSession(t, memberHTTP.URL, "fake-read", proxy)
	memberCall := func(name string, args any) string {
		called[name] = true
		return mcpCall(t, memberSession, name, args, false)
	}
	decode := func(data string, value any) {
		t.Helper()
		if err := json.Unmarshal([]byte(data), value); err != nil {
			t.Fatal(err)
		}
	}
	call("get_team_state", map[string]any{})
	call("list_teams", map[string]any{})
	var team store.Team
	decode(call("create_team", api.TeamCreateInput{Name: "MCP 虚构团队", OwnerName: "虚构负责人"}), &team)
	var invite api.TeamInviteResult
	decode(call("invite_team_member", map[string]any{"id": team.ID, "name": "MCP 虚构成员", "ttl": "1h"}), &invite)
	preview := memberCall("preview_team_join", api.TeamJoinInput{Invitation: invite.Invitation})
	if !strings.Contains(preview, invite.Fingerprint) {
		t.Fatal("preview did not preserve independent fingerprint verification")
	}
	if !strings.Contains(mcpCall(t, memberSession, "join_team", api.TeamJoinInput{Invitation: invite.Invitation}, true), "指纹") {
		t.Fatal("join accepted invitation without trusted fingerprint")
	}
	owner.clock.Add(1100)
	var joined store.JoinedTeam
	decode(memberCall("join_team", api.TeamJoinInput{Invitation: invite.Invitation, Fingerprint: invite.Fingerprint}), &joined)
	joinedList := memberCall("list_joined_teams", map[string]any{})
	for _, forbidden := range []string{"certificate", "keyRef", invite.Invitation} {
		if strings.Contains(joinedList, forbidden) {
			t.Fatal("private joined identity leaked")
		}
	}
	memberCall("change_team_address", map[string]any{"id": joined.ID, "address": invite.Address})
	call("list_team_members", map[string]any{"id": team.ID})
	input := api.ShareInput{TeamID: team.ID, DashboardID: "overview", Panels: []string{"hosts"}, Collectors: []string{"fake-host"}, Targets: []string{"web"}, Members: []string{joined.MemberID}}
	var share store.TeamShare
	decode(call("create_share", input), &share)
	if !strings.Contains(call("list_shares", map[string]any{"team": team.ID}), share.ID) {
		t.Fatal("share missing")
	}
	input.Revision = share.Revision
	decode(call("update_share", map[string]any{"id": share.ID, "teamId": input.TeamID, "dashboardId": input.DashboardID, "panels": input.Panels, "collectors": input.Collectors, "targets": input.Targets, "members": input.Members, "revision": input.Revision}), &share)
	memberCall("list_joined_shares", map[string]any{"id": joined.ID})
	definition := memberCall("get_joined_share", map[string]any{"id": joined.ID, "share": share.ID})
	if strings.Contains(definition, "SELECT ") || strings.Contains(definition, "fake-private-panel") {
		t.Fatal("shared definition escaped its scope")
	}
	panelArgs := map[string]any{"id": joined.ID, "share": share.ID, "panel": "hosts", "range": "1h", "collector": "fake-host"}
	data := memberCall("get_joined_panel", panelArgs)
	if !strings.Contains(data, "fake-host") || strings.Contains(data, "SELECT ") {
		t.Fatal("shared result missing or SQL leaked")
	}
	panelArgs["panel"] = "private"
	if !strings.Contains(mcpCall(t, readSession, "get_joined_panel", panelArgs, true), "not_found") {
		t.Fatal("member read an unshared panel")
	}
	for _, check := range []struct {
		name string
		args any
	}{
		{"create_team", api.TeamCreateInput{Name: "denied"}},
		{"invite_team_member", map[string]any{"id": team.ID, "name": "denied"}},
		{"create_share", input},
		{"revoke_share", map[string]any{"id": share.ID}},
		{"join_team", api.TeamJoinInput{Invitation: invite.Invitation, Fingerprint: invite.Fingerprint}},
	} {
		if !strings.Contains(mcpCall(t, readSession, check.name, check.args, true), "insufficient_scope") {
			t.Fatal("team admin operation bypassed HTTP scope", check.name)
		}
	}
	call("revoke_share", map[string]any{"id": share.ID})
	if !strings.Contains(mcpCall(t, readSession, "get_joined_share", map[string]any{"id": joined.ID, "share": share.ID}, true), "not_found") {
		t.Fatal("revoked share remained visible")
	}
	call("remove_team_member", map[string]any{"id": team.ID, "member": joined.MemberID})
	memberCall("leave_team", map[string]any{"id": joined.ID})
}
