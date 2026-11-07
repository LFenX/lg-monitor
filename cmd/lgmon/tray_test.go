package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/app"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/spf13/cobra"
)

type trayTestCipher struct{}

func (trayTestCipher) Encrypt(v []byte) ([]byte, error) {
	r := append([]byte(nil), v...)
	for i := range r {
		r[i] ^= 0xa5
	}
	return r, nil
}
func (p trayTestCipher) Decrypt(v []byte) ([]byte, error) { return p.Encrypt(v) }

type trayTestAutostart struct{ calls []bool }

func (s *trayTestAutostart) Set(_ string, enabled bool) error {
	s.calls = append(s.calls, enabled)
	return nil
}

func TestTraySetupCreatesConfigOnlyRotatesRevokesAndUsesFakeAutostart(t *testing.T) {
	home := t.TempDir()
	profile := app.TrayProfile{Directory: filepath.Join(t.TempDir(), "profile"), Protector: trayTestCipher{}}
	auto := &trayTestAutostart{}
	authPath := filepath.Join(t.TempDir(), "auth.db")
	deps := trayDependencies{
		Tokens: tokenDependencies{Entropy: rand.Reader, Administrator: func() (bool, error) { return true, nil }, Open: func(context.Context, string) (tokenStore, error) {
			return store.OpenAuthDB(context.Background(), authPath, time.Now)
		}},
		Profile:    func() (app.TrayProfile, error) { return profile, nil },
		Username:   func() (string, error) { return `FAKE\alice`, nil },
		Address:    func(string) (string, error) { return "http://127.0.0.1:17444", nil },
		Executable: func() (string, error) { return `C:\fake\lgmon.exe`, nil }, Autostart: auto,
		Run: func(context.Context, app.TrayBackend) error { t.Fatal("setup started native UI"); return nil },
	}
	run := func(args ...string) string {
		root := &cobra.Command{Use: "test", SilenceErrors: true, SilenceUsage: true}
		root.PersistentFlags().String("home", home, "test")
		var output bytes.Buffer
		root.SetOut(&output)
		root.SetErr(&output)
		root.AddCommand(newTrayCommand(deps))
		root.SetArgs(append([]string{"tray", "setup"}, args...))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	output := run()
	address, token, err := profile.Load()
	if err != nil || address != "http://127.0.0.1:17444" || len(token) == 0 {
		t.Fatalf("profile: %s %v", address, err)
	}
	if strings.Contains(output, string(token)) || len(auto.calls) != 0 {
		t.Fatal("setup exposed token or wrote startup without flag")
	}
	for _, name := range []string{"tray-token", "tray.json"} {
		data, err := os.ReadFile(filepath.Join(profile.Directory, name))
		if err != nil || bytes.Contains(data, token) {
			t.Fatal("plaintext credential persisted")
		}
	}
	first, err := profile.Registration()
	if err != nil {
		t.Fatal(err)
	}
	run("--autostart")
	db, err := store.OpenAuthDB(context.Background(), authPath, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.ListAPITokens(context.Background())
	if err != nil || len(rows) != 2 || len(auto.calls) != 1 || !auto.calls[0] {
		t.Fatalf("rotation/autostart: %+v %v", rows, err)
	}
	for _, row := range rows {
		if row.Name != "tray-alice" || row.Scope != "config" || (row.ID == first.TokenID && row.RevokedAt == 0) {
			t.Fatalf("incorrect authority: %+v", row)
		}
	}
	run("--remove")
	if len(auto.calls) != 1 {
		t.Fatal("remove changed startup without explicit flag")
	}
	if _, _, err := profile.Load(); err == nil {
		t.Fatal("remove retained credential")
	}
	rows, err = db.ListAPITokens(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.RevokedAt == 0 {
			t.Fatal("remove did not revoke current token")
		}
	}
	run("--remove", "--autostart")
	if len(auto.calls) != 2 || auto.calls[1] {
		t.Fatal("explicit startup removal did not use adapter")
	}
}

func TestNotifyCommandsAndStatusUseSamePersistentPause(t *testing.T) {
	home := t.TempDir()
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(home, "data", "lgmon.db"), DisableMaintenance: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	run := func(args ...string) []byte {
		var out, log bytes.Buffer
		if code := execute(append([]string{"--home", home}, args...), &out, &log); code != 0 {
			t.Fatalf("%v: %s", args, log.String())
		}
		return out.Bytes()
	}
	var p store.NotificationPause
	if err := json.Unmarshal(run("notify", "pause", "--for", "1h", "--reason", "开会"), &p); err != nil || !p.Paused || *p.By != "local" {
		t.Fatalf("pause: %+v %v", p, err)
	}
	var status statusResult
	if err := json.Unmarshal(run("status", "--json"), &status); err != nil || !status.Pause.Paused {
		t.Fatalf("status: %+v %v", status, err)
	}
	if err := json.Unmarshal(run("notify", "resume"), &p); err != nil || p.Paused || p.Until != nil {
		t.Fatalf("resume: %+v %v", p, err)
	}
}
