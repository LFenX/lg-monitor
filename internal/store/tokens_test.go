package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAPITokenPersistsHashAndIsDeniedToRuleQueries(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{DisableMaintenance: true})
	now := time.UnixMilli(2000000000000)
	authPath := filepath.Join(t.TempDir(), "auth.db")
	auth, err := OpenAuthDB(ctx, authPath, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { auth.Close() }()
	plaintext := "lgmon_" + strings.Repeat("a", 43)
	hash := sha256.Sum256([]byte(plaintext))
	created, err := auth.AddAPIToken(ctx, hash[:], "phone", "read")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	loaded, found, err := auth.Authenticate(ctx, hash[:])
	if err != nil || !found || loaded.Name != "phone" || loaded.Scope != "read" || loaded.CreatedAt != created.CreatedAt || !bytes.Equal(loaded.Hash, hash[:]) {
		t.Fatalf("token lookup: %+v %t %v", loaded, found, err)
	}
	loaded.Hash[0] ^= 0xff
	if _, found, err := auth.Authenticate(ctx, hash[:]); err != nil || !found {
		t.Fatalf("caller mutated stored hash: %v", err)
	}
	data, err := json.Marshal(created)
	if err != nil || bytes.Contains(data, []byte(plaintext)) || strings.Contains(strings.ToLower(string(data)), "hash") {
		t.Fatalf("metadata leaked token: %s %v", data, err)
	}
	var stored []byte
	if err := auth.db.QueryRowContext(ctx, `SELECT token_hash FROM api_tokens`).Scan(&stored); err != nil || !bytes.Equal(stored, hash[:]) || bytes.Contains(stored, []byte(plaintext)) {
		t.Fatalf("plaintext or wrong digest stored: %v", err)
	}
	var tokenTables int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name='api_tokens'`).Scan(&tokenTables); err != nil || tokenTables != 0 {
		t.Fatalf("token data entered monitoring database: %v", err)
	}
	queries, err := OpenRuleQueries(ctx, s.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer queries.Close()
	if _, err := queries.QueryRule(ctx, "SELECT hex(token_hash) AS key FROM api_tokens", s.now().UnixMilli()); err == nil {
		t.Fatal("rule query exposed API credentials")
	}
	unknown := sha256.Sum256([]byte("fake unknown token"))
	if _, found, err := auth.Authenticate(ctx, unknown[:]); err != nil || found {
		t.Fatalf("unknown token authenticated: %t %v", found, err)
	}
	if err := auth.Close(); err != nil {
		t.Fatal(err)
	}
	auth, err = OpenAuthDB(ctx, authPath, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	listed, err := auth.ListAPITokens(ctx)
	if err != nil || len(listed) != 1 || listed[0].LastUsedAt != now.UnixMilli() || listed[0].ID != created.ID || len(listed[0].Hash) != 0 {
		t.Fatalf("token metadata/restart: %+v %v", listed, err)
	}
	if err := auth.RevokeAPIToken(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, err := auth.Authenticate(ctx, hash[:]); err != nil || found {
		t.Fatalf("revoked token remained valid: %v", err)
	}
}
