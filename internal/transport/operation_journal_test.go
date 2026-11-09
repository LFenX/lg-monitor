package transport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
)

type operationPaths struct{}

func (operationPaths) Check(string) error             { return nil }
func (operationPaths) ProtectTree(string, bool) error { return nil }

type operationPolicyFixture struct {
	policy config.RemoteOperations
	err    error
}

func (p *operationPolicyFixture) ReadOperationPolicy(context.Context) (config.RemoteOperations, error) {
	return p.policy, p.err
}

type operationExecutorFixture struct {
	calls   atomic.Int32
	service string
	entered chan struct{}
	release chan struct{}
	err     error
}

func (e *operationExecutorFixture) Restart(ctx context.Context, service string) error {
	e.calls.Add(1)
	e.service = service
	if e.entered != nil {
		close(e.entered)
	}
	if e.release != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.release:
		}
	}
	return e.err
}

func localOperationsFixture(t *testing.T) (*collector.ServiceOperations, *OperationJournal, *operationPolicyFixture, *operationExecutorFixture, collector.OperationCommand) {
	t.Helper()
	now := func() time.Time { return time.UnixMilli(2000000000000) }
	journal, err := NewOperationJournal(OperationJournalOptions{Path: filepath.Join(t.TempDir(), "operations.db"), CollectorID: "fake-host", Paths: operationPaths{}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	policy := &operationPolicyFixture{policy: config.RemoteOperations{Enabled: true, Services: []config.ServiceGrant{{ID: "restart-demo", Service: "FakeService"}}}}
	executor := &operationExecutorFixture{}
	source, err := collector.NewServiceOperations(collector.ServiceOperationsOptions{Policy: policy, Executor: executor, Journal: journal, Now: now, Lifetime: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if executor.release != nil {
			select {
			case <-executor.release:
			default:
				close(executor.release)
			}
		}
		source.Close()
	})
	return source, journal, policy, executor, collector.OperationCommand{RequestID: strings.Repeat("a", 32), GrantID: "restart-demo", Version: policy.policy.Version(), ExpiresAt: now().Add(40 * time.Second).UnixMilli()}
}

func TestServiceOperationJournalClaimsBeforeEffectAndDeduplicatesAcrossRestart(t *testing.T) {
	source, journal, _, executor, command := localOperationsFixture(t)
	ctx := context.Background()
	first := source.Execute(ctx, command)
	if first.State != "succeeded" || executor.calls.Load() != 1 || executor.service != "FakeService" {
		t.Fatal(first, executor.calls.Load(), executor.service)
	}
	if duplicate := source.Execute(ctx, command); duplicate != first || executor.calls.Load() != 1 {
		t.Fatal("duplicate executed", duplicate)
	}
	command.GrantID = "changed-grant"
	if result := source.Execute(ctx, command); result.State != "denied" || executor.calls.Load() != 1 {
		t.Fatal("mutated identity accepted", result)
	}
	command.GrantID = "restart-demo"
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewOperationJournal(journal.options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	result, err := reopened.Result(ctx, command.RequestID)
	if err != nil || result != first {
		t.Fatal("receipt lost across restart", result, err)
	}
}

func TestServiceOperationJournalRecoveryNeverReexecutesUnfinishedCommand(t *testing.T) {
	source, journal, policy, executor, command := localOperationsFixture(t)
	ctx := context.Background()
	if _, fresh, err := journal.Claim(ctx, command, "FakeService", journal.options.Now().UnixMilli()); err != nil || !fresh {
		t.Fatal(fresh, err)
	}
	source.Close()
	reopened, err := NewOperationJournal(journal.options)
	if err != nil {
		t.Fatal(err)
	}
	next, err := collector.NewServiceOperations(collector.ServiceOperationsOptions{Policy: policy, Executor: executor, Journal: reopened, Now: journal.options.Now, Lifetime: ctx})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if result := next.Execute(ctx, command); result.State != "unknown" || executor.calls.Load() != 0 {
		t.Fatal("unfinished request executed after restart", result)
	}
}

func TestServiceOperationPolicyExpiryAndLedgerFailureHaveNoEffect(t *testing.T) {
	for _, kind := range []string{"disabled", "revoked", "changed", "policy-error", "expired", "future", "claim-failure"} {
		t.Run(kind, func(t *testing.T) {
			source, journal, policy, executor, command := localOperationsFixture(t)
			switch kind {
			case "disabled":
				policy.policy.Enabled = false
			case "revoked":
				policy.policy.Services = nil
			case "changed":
				policy.policy.Services[0].Service = "OtherService"
			case "policy-error":
				policy.err = errors.New("fake unsafe path")
			case "expired":
				command.ExpiresAt = journal.options.Now().UnixMilli()
			case "future":
				command.ExpiresAt += time.Hour.Milliseconds()
			case "claim-failure":
				if err := journal.open(context.Background(), true); err != nil {
					t.Fatal(err)
				}
				if _, err := journal.db.Exec(`CREATE TRIGGER reject_operation BEFORE INSERT ON operation_receipts BEGIN SELECT RAISE(ABORT,'fake full disk'); END`); err != nil {
					t.Fatal(err)
				}
			}
			result := source.Execute(context.Background(), command)
			if result.State != "denied" || executor.calls.Load() != 0 {
				t.Fatal("invalid operation reached executor", result, executor.calls.Load())
			}
			if kind != "claim-failure" {
				if _, err := os.Stat(journal.options.Path); !os.IsNotExist(err) {
					t.Fatal("rejected policy created journal", err)
				}
			}
		})
	}
}

func TestServiceOperationConcurrencyReadReconciliationAndPostEffectAuditFailure(t *testing.T) {
	source, journal, _, executor, command := localOperationsFixture(t)
	executor.entered, executor.release = make(chan struct{}), make(chan struct{})
	done := make(chan collector.OperationResult, 1)
	go func() { done <- source.Execute(context.Background(), command) }()
	select {
	case <-executor.entered:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}
	if result, err := source.Result(context.Background(), command.RequestID); err != nil || result.State != "running" {
		t.Fatal(result, err)
	}
	if result := source.Execute(context.Background(), command); result.State != "running" || executor.calls.Load() != 1 {
		t.Fatal("concurrent duplicate executed", result)
	}
	other := command
	other.RequestID = strings.Repeat("b", 32)
	if result := source.Execute(context.Background(), other); result.State != "denied" || executor.calls.Load() != 1 {
		t.Fatal("concurrency limit failed", result)
	}
	if _, err := journal.db.Exec(`CREATE TRIGGER reject_operation_finish BEFORE UPDATE ON operation_receipts BEGIN SELECT RAISE(ABORT,'fake audit write failure'); END`); err != nil {
		t.Fatal(err)
	}
	close(executor.release)
	select {
	case result := <-done:
		if result.State != "unknown" {
			t.Fatal("unrecorded outcome reported success", result)
		}
	case <-time.After(time.Second):
		t.Fatal("executor did not complete")
	}
	if result, err := source.Result(context.Background(), command.RequestID); err != nil || result.State != "unknown" {
		t.Fatal("completed execution without receipt stayed running", result, err)
	}
	if result := source.Execute(context.Background(), command); result.State != "unknown" || executor.calls.Load() != 1 {
		t.Fatal("audit failure caused another execution", result, executor.calls.Load())
	}
}
