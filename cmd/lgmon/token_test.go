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

type fakeTokenStore struct {
	hash        []byte
	name, scope string
	closed      int
	rows        []store.APIToken
	revoked     string
}

func (s *fakeTokenStore) AddAPIToken(_ context.Context, hash []byte, name, scope string) (store.APIToken, error) {
	s.hash = append([]byte(nil), hash...)
	s.name, s.scope = name, scope
	return store.APIToken{CreatedAt: 1234}, nil
}
func (s *fakeTokenStore) Close() error { s.closed++; return nil }
func (s *fakeTokenStore) ListAPITokens(context.Context) ([]store.APIToken, error) {
	return s.rows, nil
}
func (s *fakeTokenStore) RevokeAPIToken(_ context.Context, id string) error {
	s.revoked = id
	return nil
}

func TestTokenCommandShowsValueOnceAndOnlyPersistsHash(t *testing.T) {
	for _, scope := range []string{"read", "config", "admin"} {
		backend := &fakeTokenStore{}
		var output bytes.Buffer
		home := t.TempDir()
		root := &cobra.Command{Use: "test"}
		root.PersistentFlags().String("home", home, "fake home")
		root.SetOut(&output)
		root.SetArgs([]string{"token", "create", "--scope", scope, "--name", "phone", "--json"})
		root.AddCommand(newTokenCommand(tokenDependencies{Entropy: bytes.NewReader(make([]byte, 32)), Administrator: func() (bool, error) { return true, nil }, Open: func(_ context.Context, got string) (tokenStore, error) {
			if got != home {
				t.Fatal("home not preserved")
			}
			return backend, nil
		}}))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		var result tokenOutput
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		hash := api.TokenHash(result.Token)
		if result.Status != "ok" || result.Token == "" || result.Scope != api.Scope(scope) || result.CreatedAt != 1234 || !bytes.Equal(backend.hash, hash[:]) || backend.name != "phone" || backend.scope != scope || backend.closed != 1 {
			t.Fatalf("token result or persistence: %+v %+v", result, backend)
		}
		if strings.Count(output.String(), result.Token) != 1 {
			t.Fatal("token printed more than once")
		}
	}
}

func TestTokenManagementRequiresAdminBeforeOpeningDatabase(t *testing.T) {
	for _, args := range [][]string{{"create", "--scope", "read", "--name", "fake"}, {"create", "--scope", "config", "--name", "fake"}, {"create", "--scope", "admin", "--name", "fake"}, {"list"}, {"revoke", "fake-id"}} {
		root := &cobra.Command{Use: "test", SilenceErrors: true, SilenceUsage: true}
		root.PersistentFlags().String("home", t.TempDir(), "fake home")
		var output bytes.Buffer
		root.SetOut(&output)
		root.SetArgs(append(append([]string{"token"}, args...), "--json"))
		root.AddCommand(newTokenCommand(tokenDependencies{Entropy: bytes.NewReader(nil), Administrator: func() (bool, error) { return false, nil }, Open: func(context.Context, string) (tokenStore, error) {
			t.Fatal("unprivileged issuance opened a database")
			return nil, nil
		}}))
		if err := root.Execute(); err == nil || !strings.Contains(output.String(), "管理员身份") || !strings.Contains(output.String(), `"status":"error"`) || strings.Contains(output.String(), `"token":`) {
			t.Fatalf("privilege gate failed: %s %v", output.String(), err)
		}
	}
}

func TestTokenListAndRevokeOnlyExposeMetadata(t *testing.T) {
	for _, action := range []string{"list", "revoke"} {
		backend := &fakeTokenStore{rows: []store.APIToken{{ID: "fake-id", Name: "phone", Scope: "read", Hash: []byte("FAKE_HASH_MUST_NOT_APPEAR"), CreatedAt: 1234, LastUsedAt: 5678}}}
		root := &cobra.Command{Use: "test", SilenceErrors: true, SilenceUsage: true}
		root.PersistentFlags().String("home", t.TempDir(), "fake home")
		var output bytes.Buffer
		root.SetOut(&output)
		args := []string{"token", action, "--json"}
		if action == "revoke" {
			args = append(args, "fake-id")
		}
		root.SetArgs(args)
		root.AddCommand(newTokenCommand(tokenDependencies{Administrator: func() (bool, error) { return true, nil }, Open: func(context.Context, string) (tokenStore, error) { return backend, nil }}))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !json.Valid(output.Bytes()) || strings.Contains(output.String(), "FAKE_HASH_MUST_NOT_APPEAR") || strings.Contains(output.String(), `"hash"`) || strings.Contains(output.String(), `"token":`) || backend.closed != 1 {
			t.Fatalf("metadata leaked or handle stayed open: %s", output.String())
		}
		if action == "list" && !strings.Contains(output.String(), `"lastUsedAt":5678`) {
			t.Fatal("list omitted metadata")
		}
		if action == "revoke" && backend.revoked != "fake-id" {
			t.Fatal("revoke did not reach the store")
		}
	}
}
