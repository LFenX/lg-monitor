package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

type failingEntropy struct{ err error }

func (r failingEntropy) Read([]byte) (int, error) { return 0, r.err }

func TestTokenGenerationUsesFullEntropyAndDoesNotSerializeHash(t *testing.T) {
	for _, scope := range []Scope{ScopeRead, ScopeConfig, ScopeAdmin} {
		token, err := NewToken(scope, " phone ", bytes.NewReader(bytes.Repeat([]byte{42}, 32)))
		if err != nil || len(token.Value) != 49 || !strings.HasPrefix(token.Value, "lgmon_") || token.Name != "phone" || token.Scope != scope || token.Digest != TokenHash(token.Value) {
			t.Fatalf("token issuance: %+v %v", token, err)
		}
		data, err := json.Marshal(token)
		if err != nil || strings.Contains(strings.ToLower(string(data)), "digest") || strings.Contains(strings.ToLower(string(data)), "hash") {
			t.Fatalf("token JSON exposed internal hash: %s %v", data, err)
		}
	}
	if _, err := NewToken(ScopeRead, "phone", bytes.NewReader(make([]byte, 31))); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short entropy accepted: %v", err)
	}
	failure := errors.New("controlled entropy failure")
	if _, err := NewToken(ScopeRead, "phone", failingEntropy{failure}); !errors.Is(err, failure) {
		t.Fatalf("entropy error lost: %v", err)
	}
	if _, err := NewToken("unknown", "phone", bytes.NewReader(make([]byte, 32))); err == nil {
		t.Fatal("unknown scope accepted")
	}
}
