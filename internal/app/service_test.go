package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type serviceVault struct {
	data  []byte
	reads int
	err   error
}

func (v *serviceVault) Get(context.Context, string) ([]byte, error) { v.reads++; return v.data, v.err }

func TestServiceNotificationUsesServiceVaultClearsSecretAndAudits(t *testing.T) {
	f := newRuntimeFixture(t)
	manager := f.opts.Config.(*config.Manager)
	vault := &serviceVault{data: []byte("fake-service-secret")}
	opts := LocalOptions{Now: f.opts.Now, Logger: f.opts.Logger, NewClient: func(_ config.FeishuApp, secret []byte) (feishu.Client, error) {
		if string(secret) != "fake-service-secret" {
			t.Fatal("service did not read its own credential")
		}
		return f.client, nil
	}}
	request := &lgmonv1.ServiceTestRequest{NotifierId: "test-channel", TemplateId: "standard", TestId: "test-service-fake"}
	if _, err := serviceTestHandler(opts, manager, f.db, vault)(context.Background(), request); status.Code(err) != codes.FailedPrecondition || vault.reads != 0 {
		t.Fatalf("console accessed service credential: %v %d", err, vault.reads)
	}
	opts.ServiceIdentity = "LocalSystem"
	result, err := serviceTestHandler(opts, manager, f.db, vault)(context.Background(), request)
	if err != nil || !result.Ok || result.ExecutionIdentity != "LocalSystem" || !bytes.Equal(vault.data, make([]byte, len(vault.data))) {
		t.Fatalf("service send/clear: %+v %v", result, err)
	}
	card := receiveRuntime(t, f.client.wire)
	if !strings.Contains(string(card.Card), "这是测试消息") || card.AlertID != request.TestId {
		t.Fatal("service sent an unlabelled or wrong test card")
	}
	if attempt := receiveRuntime(t, f.db.attempts); !attempt.OK || attempt.AlertID != request.TestId || attempt.NotifierID != request.NotifierId {
		t.Fatalf("service test was not audited: %+v", attempt)
	}
}

func TestServiceCredentialFailureIsAuditedWithoutLeakingRawError(t *testing.T) {
	f := newRuntimeFixture(t)
	vault := &serviceVault{data: []byte("fake-partial-secret"), err: errors.New("fake-sensitive-canary")}
	request := &lgmonv1.ServiceTestRequest{NotifierId: "test-channel", TestId: "test-service-failure"}
	_, err := serviceTestHandler(LocalOptions{ServiceIdentity: "LocalSystem", Now: f.opts.Now, Logger: f.opts.Logger}, f.opts.Config.(*config.Manager), f.db, vault)(context.Background(), request)
	if status.Code(err) != codes.FailedPrecondition || strings.Contains(err.Error(), "fake-sensitive-canary") || !bytes.Equal(vault.data, make([]byte, len(vault.data))) {
		t.Fatalf("credential failure privacy: %v", err)
	}
	if attempt := receiveRuntime(t, f.db.attempts); attempt.OK || strings.Contains(attempt.Error, "fake-sensitive-canary") {
		t.Fatalf("unsafe credential failure audit: %+v", attempt)
	}
}
