package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type memoryConnector struct {
	listener *bufconn.Listener
	mu       sync.Mutex
	insecure []bool
}

func (m *memoryConnector) Connect(_ context.Context, _ string, configuration *tls.Config) (RPCConnection, error) {
	m.mu.Lock()
	m.insecure = append(m.insecure, configuration.InsecureSkipVerify)
	m.mu.Unlock()
	configuration = configuration.Clone()
	configuration.ServerName = "monitor.test"
	return grpc.NewClient("passthrough:///monitor.test:7443", grpc.WithTransportCredentials(credentials.NewTLS(configuration)), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return m.listener.DialContext(ctx) }))
}

type pairingFixture struct {
	ctx        context.Context
	authority  *Authority
	repository *PairDB
	connector  *memoryConnector
	receiver   *Receiver
	admin      lgmonv1.PairingClient
	millis     atomic.Int64
}

func newPairingFixture(t *testing.T) *pairingFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	f := &pairingFixture{ctx: ctx}
	f.millis.Store(time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC).UnixMilli())
	var err error
	f.authority, err = NewAuthority(ctx, AuthorityOptions{Keys: newMemoryKeys(), State: newMemoryState(), SANs: &fakeSANs{}, Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	f.repository, err = OpenPairDB(ctx, filepath.Join(t.TempDir(), "配对 # %", "pairing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.repository.Close() })
	receiver, server := NewSecureServer(f.authority, f.repository, nil)
	f.receiver = receiver
	listener := bufconn.Listen(1024 * 1024)
	f.connector = &memoryConnector{listener: listener}
	go server.Serve(listener)
	t.Cleanup(func() { server.Stop(); f.receiver.Close(); listener.Close() })
	admin, err := f.connector.Connect(ctx, "monitor.test:7443", f.authority.AdminTLS())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	f.admin = lgmonv1.NewPairingClient(admin)
	return f
}
func (f *pairingFixture) now() time.Time { return time.UnixMilli(f.millis.Load()) }
func (f *pairingFixture) code(t *testing.T, ttl time.Duration) string {
	t.Helper()
	result, err := f.admin.NewCode(f.ctx, &lgmonv1.NewCodeRequest{TtlMillis: ttl.Milliseconds()})
	if err != nil {
		t.Fatal(err)
	}
	return result.Code
}
func (f *pairingFixture) join(t *testing.T, id, code, fingerprint string) (*memoryState, *memoryKeys, string, error) {
	t.Helper()
	state, keys := newMemoryState(), newMemoryKeys()
	actual, err := Join(f.ctx, JoinOptions{Address: "monitor.test:7443", CollectorID: id, Hostname: "FakeHost", Code: code, Fingerprint: fingerprint, Keys: keys, State: state, Connector: f.connector, Now: f.now})
	return state, keys, actual, err
}

func TestPairingCorrectCodeWrongExpiredReusedAndFingerprint(t *testing.T) {
	f := newPairingFixture(t)
	code := f.code(t, 10*time.Minute)
	_, _, actual, err := f.join(t, "wrong-fingerprint", code, "00000000")
	if !errors.Is(err, ErrFingerprint) || actual != f.authority.Fingerprint() {
		t.Fatalf("fingerprint rejection: %s %v", actual, err)
	}
	if _, found, err := f.repository.Find(f.ctx, "wrong-fingerprint"); err != nil || found {
		t.Fatal("fingerprint mismatch consumed/registers pairing")
	}
	_, _, _, err = f.join(t, "needs-confirmation", code, "")
	var required *FingerprintRequired
	if !errors.As(err, &required) || required.Fingerprint != f.authority.Fingerprint() {
		t.Fatalf("missing human confirmation: %v", err)
	}
	state, keys, actual, err := f.join(t, "paired-collector", code, f.authority.Fingerprint()[:16])
	if err != nil || actual != f.authority.Fingerprint() {
		t.Fatalf("correct pairing: %v", err)
	}
	identity, err := LoadClient(f.ctx, state, keys, f.now)
	if err != nil || identity.ID != "paired-collector" {
		t.Fatalf("saved identity: %+v %v", identity, err)
	}
	if _, _, _, err := f.join(t, "reused-code", code, f.authority.Fingerprint()); status.Code(errors.Unwrap(err)) != codes.PermissionDenied {
		t.Fatalf("reused code: %v", err)
	}
	if _, _, _, err := f.join(t, "wrong-code", "ZZZZ-ZZZZ-ZZZZ", f.authority.Fingerprint()); status.Code(errors.Unwrap(err)) != codes.PermissionDenied {
		t.Fatalf("wrong code: %v", err)
	}
	expired := f.code(t, time.Second)
	f.millis.Add(2000)
	if _, _, _, err := f.join(t, "expired", expired, f.authority.Fingerprint()); status.Code(errors.Unwrap(err)) != codes.PermissionDenied {
		t.Fatalf("expired code: %v", err)
	}
	f.connector.mu.Lock()
	defer f.connector.mu.Unlock()
	// Admin, wrong fingerprint and missing confirmation use one connection each; success pins the second.
	want := []bool{false, true, true, true, false, true, false, true, false, true, false}
	if len(f.connector.insecure) != len(want) {
		t.Fatalf("connection stages: %v", f.connector.insecure)
	}
	for i, flag := range want {
		if f.connector.insecure[i] != flag {
			t.Fatalf("connection stage %d: %v", i, f.connector.insecure)
		}
	}
}

func TestAnonymousAndCollectorCannotManageAndRevokeInterruptsStream(t *testing.T) {
	f := newPairingFixture(t)
	anonymous, err := f.connector.Connect(f.ctx, "monitor.test:7443", &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, Time: f.now})
	if err != nil {
		t.Fatal(err)
	}
	defer anonymous.Close()
	if _, err := lgmonv1.NewPairingClient(anonymous).NewCode(f.ctx, &lgmonv1.NewCodeRequest{TtlMillis: 60000}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous management: %v", err)
	}
	if _, err := NewWriter(f.ctx, anonymous, Hello{CollectorID: "anonymous"}); err == nil {
		t.Fatal("anonymous ingestion accepted")
	}
	state, keys, _, err := f.join(t, "paired-collector", f.code(t, time.Minute), f.authority.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := LoadClient(f.ctx, state, keys, f.now)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := f.connector.Connect(f.ctx, "monitor.test:7443", identity.TLS())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := lgmonv1.NewPairingClient(connection).NewCode(f.ctx, &lgmonv1.NewCodeRequest{TtlMillis: 60000}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("collector management: %v", err)
	}
	if _, err := NewWriter(f.ctx, connection, Hello{CollectorID: "impersonated"}); err == nil {
		t.Fatal("certificate/Hello mismatch accepted")
	}
	writer, err := NewWriter(f.ctx, connection, Hello{CollectorID: identity.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	message := wireMessage(1)
	message.CollectorID = identity.ID
	result := make(chan error, 1)
	go func() { result <- writer.Write(f.ctx, message) }()
	received, err := f.receiver.Read(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.RevokeCollector(f.ctx, &lgmonv1.RevokeCollectorRequest{Id: identity.ID}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("revoked stream: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("revocation did not interrupt pending stream")
	}
	f.receiver.Acknowledge(received, nil)
	if _, err := NewWriter(f.ctx, connection, Hello{CollectorID: identity.ID}); err == nil {
		t.Fatal("revoked certificate reconnected on existing TLS connection")
	}
	connection.Close()
	newConnection, err := f.connector.Connect(f.ctx, "monitor.test:7443", identity.TLS())
	if err != nil {
		t.Fatal(err)
	}
	defer newConnection.Close()
	if _, err := NewWriter(f.ctx, newConnection, Hello{CollectorID: identity.ID}); err == nil {
		t.Fatal("revoked certificate accepted by fresh TLS handshake")
	}
	listed, err := f.admin.ListCollectors(f.ctx, &lgmonv1.ListCollectorsRequest{})
	if err != nil || len(listed.Collectors) != 1 || !listed.Collectors[0].Revoked {
		t.Fatalf("revoked listing: %+v %v", listed, err)
	}
}
