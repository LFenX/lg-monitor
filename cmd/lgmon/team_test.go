package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/spf13/cobra"
)

func runTeamCLI(t *testing.T, call teamCall, input string, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "lgmon", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("home", "fake-home", "")
	root.AddCommand(newTeamCommand(call), newShareCommand(call))
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetIn(strings.NewReader(input))
	root.SetArgs(args)
	err := root.Execute()
	return output.String(), err
}

func TestTeamCLIInvitesStayInStdinAndEmptyScopesNeverBroaden(t *testing.T) {
	calls := 0
	call := func(_ context.Context, home string, op api.Operation, params map[string]string, body any) (json.RawMessage, error) {
		calls++
		if home != "fake-home" {
			t.Fatal("wrong home")
		}
		if op == api.PreviewTeamJoin {
			input := body.(api.TeamJoinInput)
			if input.Invitation != "fake-invitation-canary" || input.Fingerprint != "" {
				t.Fatal("stdin changed", input)
			}
			return json.RawMessage(`{"address":"https://fake.example.test","fingerprint":"ABCD-1234-ABCD-1234","teamId":"team-fake"}`), nil
		}
		return json.RawMessage(`{"ok":true}`), nil
	}
	out, err := runTeamCLI(t, call, "fake-invitation-canary\n", "team", "join", "--invite-stdin", "--preview", "--json")
	if err != nil || !json.Valid([]byte(out)) || !strings.Contains(out, "team-fake") || calls != 1 {
		t.Fatal(out, err, calls)
	}
	if _, err := runTeamCLI(t, call, "fake-invitation-canary", "team", "join", "--invite-stdin", "--json"); err == nil || calls != 1 {
		t.Fatal("join proceeded without fingerprint", err, calls)
	}
	if _, err := runTeamCLI(t, call, "", "share", "create", "team-fake", "--dashboard", "overview", "--collectors=", "--json"); err == nil || calls != 1 {
		t.Fatal("empty collector scope became unrestricted", err, calls)
	}
	if _, err := runTeamCLI(t, call, "", "team", "invite", "team-fake", "--json"); err == nil || calls != 1 {
		t.Fatal("unnamed device invite proceeded", err, calls)
	}
}

func TestShareCLIUpdatePreservesOmittedDimensionsAndRevision(t *testing.T) {
	original := store.TeamShare{ID: "share-fake", TeamID: "team-fake", DashboardID: "overview", Panels: []string{"first"}, ShareScope: store.ShareScope{Collectors: []string{"fake-host"}, Targets: []string{"web"}}, Members: []string{"member-fake"}, Revision: 7}
	updates := 0
	call := func(_ context.Context, _ string, op api.Operation, params map[string]string, body any) (json.RawMessage, error) {
		if op == api.ListShares {
			value, err := json.Marshal([]store.TeamShare{original})
			return value, err
		}
		if op != api.UpdateShare || params["id"] != original.ID {
			t.Fatal("wrong update route", op, params)
		}
		updates++
		input := body.(api.ShareInput)
		if input.Collectors != nil || len(input.Targets) != 1 || input.Targets[0] != "web" || len(input.Panels) != 1 || input.Members[0] != "member-fake" || input.Revision != 7 {
			t.Fatal("omitted scope fields were changed", input)
		}
		return json.RawMessage(`{"id":"share-fake","revision":8}`), nil
	}
	result, err := runTeamCLI(t, call, "", "share", "update", original.ID, "--all-collectors", "--json")
	if err != nil || updates != 1 || !strings.Contains(result, `"revision":8`) {
		t.Fatal(result, err, updates)
	}
	call = func(context.Context, string, api.Operation, map[string]string, any) (json.RawMessage, error) {
		return json.RawMessage(`{"error":{"code":"share_changed","message":"请刷新后重试"}}`), nil
	}
	result, err = runTeamCLI(t, call, "", "team", "list", "--json")
	if err == nil || !json.Valid([]byte(result)) || !strings.Contains(result, "share_changed") {
		t.Fatal("structured failure lost", result, err)
	}
}

func TestTeamCLIOutputsListResultsAsArrays(t *testing.T) {
	call := func(context.Context, string, api.Operation, map[string]string, any) (json.RawMessage, error) {
		return json.RawMessage(`[{"id":"team-test"}]`), nil
	}
	for _, args := range [][]string{{"team", "list", "--json"}, {"team", "members", "team-test", "--json"}, {"team", "joined", "--json"}, {"share", "list", "--json"}} {
		out, err := runTeamCLI(t, call, "", args...)
		if err != nil || !strings.HasPrefix(strings.TrimSpace(out), "[") {
			t.Fatal("list response rejected", args, out, err)
		}
	}
}
