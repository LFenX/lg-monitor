package app

import (
	"crypto/rand"
	"testing"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
)

func TestLocalPageScopeFollowsInstallKindAndSetting(t *testing.T) {
	for _, test := range []struct {
		access   string
		personal bool
		want     api.Scope
	}{
		{"", true, api.ScopeConfig},
		{"", false, api.ScopeRead},
		{"read", true, api.ScopeRead},
		{"read", false, api.ScopeRead},
		{"config", false, api.ScopeConfig},
	} {
		if got := localScope(config.Settings{LocalAccess: test.access}, test.personal); got != test.want {
			t.Fatalf("localAccess=%q personal=%v: got %s want %s", test.access, test.personal, got, test.want)
		}
	}
	auth, err := api.NewAuthenticator(api.AuthOptions{Tokens: api.TokenLookupFunc(nil), Hosts: &api.HostPolicy{}, Entropy: rand.Reader})
	if err != nil {
		t.Fatal(err)
	}
	if auth.LocalScope() != api.ScopeRead {
		t.Fatal("the page opened on this computer must start read-only")
	}
	if err := auth.SetLocalScope(api.ScopeAdmin); err == nil || auth.LocalScope() != api.ScopeRead {
		t.Fatal("the local page must never become admin")
	}
	if err := auth.SetLocalScope(api.ScopeConfig); err != nil || auth.LocalScope() != api.ScopeConfig {
		t.Fatal("config scope was not applied")
	}
}
