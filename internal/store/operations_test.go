package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
)

func operationStoreIntent() OperationRequest {
	return OperationRequest{RequestID: strings.Repeat("a", 32), CollectorID: "fake-host", GrantID: "restart-demo", Version: strings.Repeat("b", 64), Service: "FakeService", Actor: "fake-admin", Reason: "本人确认的虚构演示"}
}

func TestOperationIntentTransactionRecoveryAndIsolation(t *testing.T) {
	ctx := context.Background()
	db := openStore(t, Options{DisableMaintenance: true})
	intent := operationStoreIntent()
	if _, err := db.db.Exec(`CREATE TRIGGER reject_operation_intent BEFORE INSERT ON operation_requests BEGIN SELECT RAISE(ABORT,'fake full disk'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ClaimOperation(ctx, intent, "fake-hash"); err == nil {
		t.Fatal("failed intent reported persistence")
	}
	if _, err := db.OperationRequest(ctx, intent.RequestID, ""); !errors.Is(err, collector.ErrOperationMissing) {
		t.Fatal("partial intent committed", err)
	}
	if _, err := db.db.Exec(`DROP TRIGGER reject_operation_intent`); err != nil {
		t.Fatal(err)
	}
	first, fresh, err := db.ClaimOperation(ctx, intent, "fake-hash")
	if err != nil || !fresh || first.State != "running" {
		t.Fatal(first, fresh, err)
	}
	if _, _, err := db.ClaimOperation(ctx, intent, "changed-hash"); !errors.Is(err, collector.ErrOperationConflict) {
		t.Fatal("changed identity accepted", err)
	}
	if err := db.RecoverOperations(ctx); err != nil {
		t.Fatal(err)
	}
	unknown, err := db.OperationRequest(ctx, intent.RequestID, "fake-hash")
	if err != nil || unknown.State != "unknown" {
		t.Fatal(unknown, err)
	}
	view, err := OpenView(ctx, db.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	if _, err := view.Query(ctx, `SELECT request_id,state FROM operation_requests`); err != nil {
		t.Fatal("direct read failed", err)
	}
	for _, scope := range []ShareScope{{}, {Collectors: []string{"fake-host"}}, {Targets: []string{"demo"}}} {
		if _, err := view.QueryShare(ctx, `SELECT request_id FROM operation_requests`, scope, nil); err == nil {
			t.Fatal("operational audit escaped through sharing", scope)
		}
	}
	queries, err := OpenRuleQueries(ctx, db.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer queries.Close()
	if _, err := queries.QueryRule(ctx, `SELECT request_id AS key FROM operation_requests`, db.now().UnixMilli()); err == nil {
		t.Fatal("current operation state participated in historical rule replay")
	}
	result := collector.OperationResult{RequestID: intent.RequestID, GrantID: intent.GrantID, Version: intent.Version, Service: intent.Service, State: "succeeded", StartedAt: db.now().UnixMilli(), FinishedAt: db.now().UnixMilli(), Message: "夹具已核对运行"}
	if _, err := db.db.Exec(`CREATE TRIGGER reject_operation_outcome BEFORE UPDATE ON operation_requests BEGIN SELECT RAISE(ABORT,'fake audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.FinishOperation(ctx, intent.RequestID, result); err == nil {
		t.Fatal("failed outcome reported persistence")
	}
	stored, err := db.OperationRequest(ctx, intent.RequestID, "")
	if err != nil || stored.State != "unknown" {
		t.Fatal("failed outcome partially committed", stored, err)
	}
}
