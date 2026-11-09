package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
)

type operationSourceFixture struct {
	policy           collector.OperationPolicy
	result           collector.OperationResult
	err              error
	policyErr        error
	calls            atomic.Int32
	entered, release chan struct{}
}

type rejectedOperationRepository struct{ OperationRepository }

func (rejectedOperationRepository) ClaimOperation(context.Context, store.OperationRequest, string) (store.OperationRequest, bool, error) {
	return store.OperationRequest{}, false, errors.New("fake intent storage failure")
}

func TestServiceOperationMonitorIntentFailureNeverDispatches(t *testing.T) {
	f, source, input := monitorOperationsFixture(t)
	f.backend.options.Operations.repository = rejectedOperationRepository{OperationRepository: f.db}
	if _, err := f.backend.options.Operations.Execute(context.Background(), "fake-host", "fake-admin", input); err == nil || source.calls.Load() != 0 {
		t.Fatal("unrecorded intent reached remote executor", err, source.calls.Load())
	}
}

func (s *operationSourceFixture) OperationPolicy(context.Context, string) (collector.OperationPolicy, error) {
	return s.policy, s.policyErr
}
func (s *operationSourceFixture) ExecuteOperation(ctx context.Context, _ string, command collector.OperationCommand) (collector.OperationResult, error) {
	s.calls.Add(1)
	if s.entered != nil {
		close(s.entered)
	}
	if s.release != nil {
		select {
		case <-ctx.Done():
			return collector.OperationResult{}, &transport.OperationDispatchError{Sent: true, Cause: ctx.Err()}
		case <-s.release:
		}
	}
	result := s.result
	result.RequestID, result.GrantID, result.Version = command.RequestID, command.GrantID, command.Version
	return result, s.err
}
func (s *operationSourceFixture) OperationResult(context.Context, string, string) (collector.OperationResult, error) {
	return s.result, s.err
}

func monitorOperationsFixture(t *testing.T) (*apiFixture, *operationSourceFixture, api.ServiceOperationInput) {
	t.Helper()
	f := newAPIFixture(t)
	source, input := attachMonitorOperations(t, f)
	return f, source, input
}

func attachMonitorOperations(t *testing.T, f *apiFixture) (*operationSourceFixture, api.ServiceOperationInput) {
	t.Helper()
	policy := config.RemoteOperations{Enabled: true, Services: []config.ServiceGrant{{ID: "restart-demo", Service: "FakeService"}}}
	source := &operationSourceFixture{policy: collector.OperationPolicy{Enabled: true, Services: policy.Services, Version: policy.Version(), Reason: "虚构本机授权"}, result: collector.OperationResult{RequestID: strings.Repeat("a", 32), GrantID: "restart-demo", Version: policy.Version(), Service: "FakeService", State: "succeeded", StartedAt: f.now.UnixMilli(), FinishedAt: f.now.UnixMilli(), Message: "夹具核对服务已运行"}}
	manager, err := NewOperationManager(context.Background(), f.db, source)
	if err != nil {
		t.Fatal(err)
	}
	f.backend.options.Operations = manager
	input := api.ServiceOperationInput{RequestID: strings.Repeat("a", 32), GrantID: "restart-demo", Version: policy.Version(), Reason: "本人确认的虚构演示", Confirmed: true}
	return source, input
}

func TestServiceOperationHTTPPermissionsConfirmationAndImmutableRequest(t *testing.T) {
	f, source, input := monitorOperationsFixture(t)
	path := "/api/v1/collectors/fake-host/operations"
	f.request(t, "GET", path, "read", "", 200)
	data, _ := json.Marshal(input)
	for _, role := range []string{"read", "config"} {
		f.request(t, "POST", path, role, string(data), 403)
	}
	input.Confirmed = false
	bad, _ := json.Marshal(input)
	f.request(t, "POST", path, "admin", string(bad), 400)
	input.Confirmed = true
	f.request(t, "POST", path, "admin", string(data), 200)
	f.request(t, "POST", path, "admin", string(data), 200)
	if source.calls.Load() != 1 {
		t.Fatal("same request restarted twice")
	}
	f.request(t, "GET", "/api/v1/operations/"+input.RequestID, "read", "", 200)
	input.Reason = "different intent"
	bad, _ = json.Marshal(input)
	f.request(t, "POST", path, "admin", string(bad), 409)
	input.RequestID = strings.Repeat("b", 32)
	input.Version = strings.Repeat("c", 64)
	bad, _ = json.Marshal(input)
	f.request(t, "POST", path, "admin", string(bad), 409)
	if source.calls.Load() != 1 {
		t.Fatal("rejected input reached service executor")
	}
}

func TestServiceOperationUnknownReconcilesReadOnlyAndRestartDoesNotResend(t *testing.T) {
	f, source, input := monitorOperationsFixture(t)
	manager := f.backend.options.Operations
	source.err = &transport.OperationDispatchError{Sent: true, Cause: errors.New("fake lost reply")}
	result, err := manager.Execute(context.Background(), "fake-host", "fake-admin", input)
	if err == nil || result.State != "unknown" || source.calls.Load() != 1 {
		t.Fatal(result, err)
	}
	if _, err := NewOperationManager(context.Background(), f.db, source); err != nil {
		t.Fatal(err)
	}
	if again, err := manager.Execute(context.Background(), "fake-host", "fake-admin", input); err == nil || again.State != "unknown" || source.calls.Load() != 1 {
		t.Fatal("unknown resent", again, err)
	}
	source.err = nil
	updated, err := manager.Get(context.Background(), input.RequestID)
	if err != nil || updated.State != "succeeded" || source.calls.Load() != 1 {
		t.Fatal("read-only reconciliation failed", updated, err)
	}
}

func TestServiceOperationHTTPPreservesLocalGrantsAndRejectsExtraCommandFields(t *testing.T) {
	f, source, input := monitorOperationsFixture(t)
	policy := config.RemoteOperations{Enabled: true, Services: source.policy.Services}
	settings := f.manager.Snapshot().Settings
	settings.RemoteOperations = policy
	data, _ := json.Marshal(settings)
	f.request(t, "PUT", "/api/v1/config/settings", "admin", string(data), 400)
	body, _ := json.Marshal(input)
	body = append(body[:len(body)-1], []byte(`,"service":"OtherService"}`)...)
	f.request(t, "POST", "/api/v1/collectors/fake-host/operations", "admin", string(body), 400)
	if source.calls.Load() != 0 {
		t.Fatal("HTTP bypassed local policy")
	}
}

func TestServiceOperationConcurrentSameIDDoesNotDuplicateDispatch(t *testing.T) {
	f, source, input := monitorOperationsFixture(t)
	source.entered, source.release = make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-source.release:
		default:
			close(source.release)
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := f.backend.options.Operations.Execute(context.Background(), "fake-host", "fake-admin", input)
		done <- err
	}()
	<-source.entered
	if result, err := f.backend.options.Operations.Execute(context.Background(), "fake-host", "fake-admin", input); err == nil || result.State != "running" {
		t.Fatal("duplicate started execution", result, err)
	}
	close(source.release)
	if err := <-done; err != nil || source.calls.Load() != 1 {
		t.Fatal(err, source.calls.Load())
	}
}
