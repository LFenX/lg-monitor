package transport

import (
	"context"
	"crypto/tls"
	"sync/atomic"
	"testing"
	"time"

	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestServiceTestRequiresAdminAndConfiguredServiceContext(t *testing.T) {
	f := newPairingFixture(t)
	request := &lgmonv1.ServiceTestRequest{NotifierId: "fake-channel", TestId: "test-fake-nonce"}
	if _, err := f.admin.TestFeishu(f.ctx, request); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("console context accepted: %v", err)
	}
	var called atomic.Int32
	f.receiver.SetServiceTest(func(_ context.Context, got *lgmonv1.ServiceTestRequest) (*lgmonv1.ServiceTestResponse, error) {
		called.Add(1)
		if got.NotifierId != request.NotifierId || got.TestId != request.TestId {
			t.Error("management changed test request")
		}
		return &lgmonv1.ServiceTestResponse{Ok: true, ExecutionIdentity: "LocalSystem", MessageId: "om_fake_service"}, nil
	})
	result, err := f.admin.TestFeishu(f.ctx, request)
	if err != nil || !result.Ok || result.ExecutionIdentity != "LocalSystem" {
		t.Fatalf("admin service test: %+v %v", result, err)
	}
	state, keys, _, err := f.join(t, "fake-collector", f.code(t, time.Minute), f.authority.Fingerprint())
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
	if _, err := lgmonv1.NewPairingClient(connection).TestFeishu(f.ctx, request); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("collector sent service notification: %v", err)
	}
	anonymous, err := f.connector.Connect(f.ctx, "monitor.test:7443", &tls.Config{MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, Time: f.now})
	if err != nil {
		t.Fatal(err)
	}
	defer anonymous.Close()
	if _, err := lgmonv1.NewPairingClient(anonymous).TestFeishu(f.ctx, request); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous sent service notification: %v", err)
	}
	if called.Load() != 1 {
		t.Fatal("unauthorized request reached sender")
	}
}
