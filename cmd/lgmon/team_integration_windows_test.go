//go:build integration && windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/app"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/secrets"
	"github.com/LFenX/lg-monitor/internal/store"
)

func startTeamCLIMonitor(t *testing.T, home string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- app.RunLocal(ctx, app.LocalOptions{Home: home, Role: "monitor", Listen: "127.0.0.1:0", NoTray: true, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), HTTPReady: func(address string) { ready <- address }})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error("temporary monitor shutdown", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("temporary monitor failed to stop")
		}
	})
	select {
	case <-ready:
	case err := <-done:
		done <- err
		t.Fatal("temporary monitor startup", err)
	case <-time.After(20 * time.Second):
		t.Fatal("temporary monitor readiness timeout")
	}
}

func nativeTeamCLI(t *testing.T, home, input string, args ...string) ([]byte, error) {
	t.Helper()
	root := newRootCommand()
	root.SetArgs(append([]string{"--home", home}, args...))
	root.SetIn(strings.NewReader(input))
	var output, diagnostic bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&diagnostic)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := root.ExecuteContext(ctx)
	if err != nil && output.Len() == 0 {
		t.Log("CLI diagnostic", diagnostic.String())
	}
	return output.Bytes(), err
}

func TestNativeTeamTwoHomesCLISharingProtectedKeysAndNoResultPersistence(t *testing.T) {
	runNativeTeamTwoHomes(t, "fixture-host", "web", "hosts")
}

func TestNativeTeamLongConfiguredIDs(t *testing.T) {
	runNativeTeamTwoHomes(t, "Legacy-HOST", strings.Repeat("target-", 700), strings.Repeat("card-", 850))
}

func runNativeTeamTwoHomes(t *testing.T, collectorID, targetID, panelID string) {
	t.Helper()
	ctx := context.Background()
	owner, member := t.TempDir(), t.TempDir()
	for _, home := range []string{owner, member} {
		if err := os.MkdirAll(filepath.Join(home, "config", "dashboards"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "config", "settings.yaml"), []byte("httpListen: 127.0.0.1:0\nteamListen: 127.0.0.1:0\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dashboard := `id: shared
title: 测试共享
refresh: 10s
defaultRange: 1h
panels:
- id: hosts
  type: table
  title: 已分享主机
  width: 6
  query: SELECT collector_id,target_id,'unique-shared-rows-91ac' AS marker FROM target_status
- id: total
  type: stat
  title: 数量
  width: 6
  query: SELECT count(*) AS value FROM target_status
- id: private
  type: table
  title: 未分享
  width: 12
  query: SELECT 'unique-private-card-b7e0' AS value
`
	dashboard = strings.Replace(dashboard, "- id: hosts\n", "- id: "+panelID+"\n", 1)
	if err := os.WriteFile(filepath.Join(owner, "config", "dashboards", "shared.yaml"), []byte(dashboard), 0600); err != nil {
		t.Fatal(err)
	}
	configured, err := json.Marshal(config.TargetsFile{Targets: []config.Target{{ID: targetID, Match: config.Match{Process: "fake.exe"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owner, "config", "targets.yaml"), configured, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(owner, "data", "lgmon.db"), DisableMaintenance: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{collectorID, "outside-host"} {
		if err := db.RegisterCollector(ctx, store.CollectorRecord{ID: host, Hostname: host, FirstSeen: 1, LastSeen: time.Now().UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		if err := db.Write(ctx, pipeline.Message{CollectorID: host, Seq: 1, TS: time.Now().UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: targetID, State: "up"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	startTeamCLIMonitor(t, owner)
	startTeamCLIMonitor(t, member)
	call := func(home, input string, out any, args ...string) []byte {
		t.Helper()
		data, err := nativeTeamCLI(t, home, input, append(args, "--json")...)
		if err != nil {
			t.Fatal("CLI", args, err, string(data))
		}
		if out != nil {
			if err := json.Unmarshal(data, out); err != nil {
				t.Fatal("CLI JSON", err)
			}
		}
		return data
	}
	var team store.Team
	call(owner, "", &team, "team", "create", "工程夹具团队", "--owner", "工程负责人")
	var invite api.TeamInviteResult
	call(owner, "", &invite, "team", "invite", team.ID, "--name", "测试成员设备")
	var preview api.TeamJoinPreview
	call(member, invite.Invitation, &preview, "team", "join", "--invite-stdin", "--preview")
	if preview.Fingerprint != invite.Fingerprint {
		t.Fatal("CLI preview fingerprint mismatch")
	}
	var joined store.JoinedTeam
	call(member, invite.Invitation, &joined, "team", "join", "--invite-stdin", "--fingerprint", invite.Fingerprint)
	var share store.TeamShare
	call(owner, "", &share, "share", "create", team.ID, "--dashboard", "shared", "--panels", panelID+",total", "--collectors", collectorID, "--targets", targetID, "--members", joined.MemberID)
	definition := call(member, "", nil, "team", "view", joined.ID, share.ID)
	if bytes.Contains(definition, []byte("SELECT")) || bytes.Contains(definition, []byte("unique-private")) {
		t.Fatal("CLI member received private SQL or card")
	}
	rows := call(member, "", nil, "team", "view", joined.ID, share.ID, panelID, "--collector", collectorID)
	if !bytes.Contains(rows, []byte("unique-shared-rows-91ac")) || bytes.Contains(rows, []byte("outside-host")) {
		t.Fatal("CLI live row scope escaped", string(rows))
	}
	if _, err := nativeTeamCLI(t, member, "", "team", "view", joined.ID, share.ID, "private", "--json"); err == nil {
		t.Fatal("CLI read unshared card")
	}
	var roster []store.TeamMember
	call(owner, "", &roster, "team", "members", team.ID)
	if len(roster) != 1 || roster[0].AccessCount < 2 {
		t.Fatal("CLI visit metadata", roster)
	}
	vault, err := secrets.New(member, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	names, err := vault.List(ctx)
	if err != nil || len(names) != 4 {
		t.Fatal("protected device key not persisted", len(names), err)
	}
	if err := secrets.NativePathPolicy().Check(filepath.Join(member, "data", "auth.db")); err != nil {
		t.Fatal("member identity ACL", err)
	}
	if err := filepath.WalkDir(member, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, canary := range []string{"unique-shared-rows-91ac", "unique-private-card-b7e0", invite.Invitation} {
			if bytes.Contains(data, []byte(canary)) {
				t.Errorf("member persisted result, SQL or invitation in %s", filepath.Base(path))
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	call(owner, "", nil, "share", "update", share.ID, "--panels", "total")
	if _, err := nativeTeamCLI(t, member, "", "team", "view", joined.ID, share.ID, panelID, "--json"); err == nil {
		t.Fatal("CLI stale card authorization remained")
	}
	call(owner, "", nil, "share", "revoke", share.ID)
	if _, err := nativeTeamCLI(t, member, "", "team", "view", joined.ID, share.ID, "--json"); err == nil {
		t.Fatal("CLI revoked share remained")
	}
	call(owner, "", nil, "team", "remove", team.ID, joined.MemberID)
	if _, err := nativeTeamCLI(t, member, "", "team", "shared", joined.ID, "--json"); err == nil {
		t.Fatal("CLI removed device retained connection")
	}
}
