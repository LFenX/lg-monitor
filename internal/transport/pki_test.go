package transport

import (
	"bytes"
	"context"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/base64"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

type memoryKeys struct {
	mu    sync.Mutex
	data  map[string][]byte
	reads [][]byte
}

func newMemoryKeys() *memoryKeys { return &memoryKeys{data: make(map[string][]byte)} }
func (m *memoryKeys) Set(_ context.Context, name string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[name] = bytes.Clone(data)
	return nil
}
func (m *memoryKeys) Get(_ context.Context, name string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.data[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	copy := bytes.Clone(data)
	m.reads = append(m.reads, copy)
	return copy, nil
}

type memoryState struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemoryState() *memoryState { return &memoryState{data: make(map[string][]byte)} }
func (m *memoryState) Load(_ context.Context, name string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.data[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return bytes.Clone(data), nil
}
func (m *memoryState) Save(_ context.Context, name string, data []byte, overwrite bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[name]; ok && !overwrite {
		return os.ErrExist
	}
	m.data[name] = bytes.Clone(data)
	return nil
}

type fakeSANs struct{ calls int }

func (f *fakeSANs) Discover(context.Context) (SANs, error) {
	f.calls++
	return SANs{Names: []string{"monitor.test", "localhost"}, IPs: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("192.0.2.4"), net.ParseIP("100.64.0.9")}}, nil
}

func TestAuthorityPersistsP256TenYearCAAndNeverStoresPlainKeys(t *testing.T) {
	ctx := context.Background()
	keys, state, sans := newMemoryKeys(), newMemoryState(), &fakeSANs{}
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	opts := AuthorityOptions{Keys: keys, State: state, SANs: sans, Now: func() time.Time { return now }}
	authority, err := NewAuthority(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if authority.key.Curve != elliptic.P256() || !authority.ca.IsCA || !authority.ca.NotAfter.Equal(now.AddDate(10, 0, 0)) || len(authority.Fingerprint()) != 64 {
		t.Fatal("CA algorithm/lifetime/fingerprint mismatch")
	}
	for _, name := range []string{"monitor.test", "localhost", "192.0.2.4", "100.64.0.9"} {
		if err := authority.server.Leaf.VerifyHostname(name); err != nil {
			t.Fatal(err)
		}
	}
	if authority.server.Leaf.IsCA || authority.admin.Leaf.Subject.CommonName != adminName {
		t.Fatal("leaf identity/CA constraint mismatch")
	}
	before := bytes.Clone(state.data["authority"])
	loaded, err := NewAuthority(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Fingerprint() != authority.Fingerprint() || sans.calls != 1 || !bytes.Equal(before, state.data["authority"]) {
		t.Fatal("existing authority was regenerated")
	}
	for _, plain := range keys.data {
		if bytes.Contains(before, []byte(base64.StdEncoding.EncodeToString(plain))) {
			t.Fatal("public state contains a private key")
		}
	}
	for _, read := range keys.reads {
		if !bytes.Equal(read, make([]byte, len(read))) {
			t.Fatal("owned private key DER was not cleared")
		}
	}
	opts.ExistingOnly = true
	opts.State = newMemoryState()
	if _, err := NewAuthority(ctx, opts); err == nil {
		t.Fatal("read-only authority load created a new CA")
	}
	if sans.calls != 1 {
		t.Fatal("read-only load discovered/generates credentials")
	}
	opts.State = state
	state.data["authority"] = []byte(`{"version":1,"unexpected":true}`)
	if _, err := NewAuthority(ctx, opts); err == nil {
		t.Fatal("malformed authority silently replaced")
	}
}

func TestCertificateRequestCannotEscalateToAdminOrCA(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	authority, err := NewAuthority(ctx, AuthorityOptions{Keys: newMemoryKeys(), State: newMemoryState(), SANs: &fakeSANs{}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := NewClientRequest(adminName, nil); err == nil {
		t.Fatal("reserved admin name accepted")
	}
	_, csr, err := NewClientRequest("fake-collector", nil)
	if err != nil {
		t.Fatal(err)
	}
	id, certificate, err := authority.SignCSR(csr)
	if err != nil || id != "fake-collector" {
		t.Fatalf("CSR: %s %v", id, err)
	}
	leaf, err := parseCertificate(certificate)
	if err != nil || leaf.IsCA || len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("client privileges: %+v %v", leaf, err)
	}
	csr[len(csr)-1] ^= 1
	if _, _, err := authority.SignCSR(csr); err == nil {
		t.Fatal("forged CSR accepted")
	}
}
