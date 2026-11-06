package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestQueryConnectionIsReadOnlyEvenWhenQueryOnlyIsDisabledInternally(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{DisableMaintenance: true})
	v, err := OpenView(ctx, s.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	var queryOnly int
	if err := v.db.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&queryOnly); err != nil || queryOnly != 1 {
		t.Fatalf("query_only is not enabled: %d %v", queryOnly, err)
	}
	for _, query := range []string{"INSERT INTO collectors(id) VALUES('fake-write')", "DELETE FROM alerts", "CREATE TABLE unsafe(id)", "PRAGMA query_only=OFF", "ATTACH DATABASE ':memory:' AS extra", "SELECT 1; SELECT 2", "SELECT 1; DELETE FROM alerts", "WITH x AS (SELECT 1) DELETE FROM alerts RETURNING id", `SELECT "load_extension"('unused')`, `SELECT readfile('unused')`, `SELECT writefile('unused','data')`} {
		if _, err := v.Query(ctx, query); err == nil {
			t.Fatalf("unsafe SQL accepted: %s", query)
		}
	}
	if _, err := v.db.ExecContext(ctx, `PRAGMA query_only=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := v.db.ExecContext(ctx, `INSERT INTO collectors(id) VALUES('fake-write')`); err == nil {
		t.Fatal("mode=ro did not prevent a write after bypassing the API guard")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM collectors WHERE id='fake-write'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("read-only tests changed monitor data")
	}
}

func TestUserQueryTruncatesOnlyAboveTenThousandAndEncodesBlobAndNull(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{DisableMaintenance: true})
	v, err := OpenViewWithClock(ctx, s.maintenanceView.path, func() time.Time { return time.UnixMilli(123456789) })
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	for _, n := range []int{10000, 10001} {
		text := "WITH RECURSIVE x(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM x WHERE n<" + fmt.Sprint(n) + ") SELECT n FROM x"
		result, err := v.Query(ctx, text)
		if err != nil || len(result.Rows) != 10000 || result.Truncated != (n > 10000) {
			t.Fatalf("row cap: n=%d rows=%d truncated=%t err=%v", n, len(result.Rows), result.Truncated, err)
		}
	}
	result, err := v.Query(ctx, `/* comment */ WITH x(v) AS (SELECT ';') SELECT v,NULL AS empty,x'00ff' AS payload,:now AS clock FROM x; -- tail`)
	if err != nil || len(result.Rows) != 1 || result.Rows[0][3] != int64(123456789) {
		t.Fatalf("SQL literals or injected clock: %+v %v", result, err)
	}
	data, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(data), `null,"AP8="`) {
		t.Fatalf("BLOB or null JSON: %s %v", data, err)
	}
	if _, err := v.Query(ctx, `SELECT 1e999`); err == nil || !strings.Contains(err.Error(), "非有限数值") {
		t.Fatal("non-finite query value would break JSON output")
	}
}

func TestUserQueryEnforcesFiveSecondBudgetAndCanReuseInterruptedConnection(t *testing.T) {
	s := openStore(t, Options{DisableMaintenance: true})
	v, err := OpenView(context.Background(), s.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	started := time.Now()
	_, err = v.Query(context.Background(), `WITH RECURSIVE x(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM x) SELECT sum(n) FROM x`)
	elapsed := time.Since(started)
	if !errors.Is(err, context.DeadlineExceeded) || elapsed < QueryBudget-100*time.Millisecond || elapsed > QueryBudget+5*time.Second {
		t.Fatalf("query budget: %s %v", elapsed, err)
	}
	result, err := v.Query(context.Background(), `SELECT 42`)
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != int64(42) {
		t.Fatalf("interrupted connection was not reusable: %+v %v", result, err)
	}
}

func TestDedicatedQueryReaderDoesNotHoldOtherReadsOrWriter(t *testing.T) {
	s := openStore(t, Options{DisableMaintenance: true})
	query, err := OpenView(context.Background(), s.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer query.Close()
	status, err := OpenView(context.Background(), s.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer status.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := query.Query(ctx, `WITH RECURSIVE x(n) AS(SELECT 1 UNION ALL SELECT n+1 FROM x) SELECT sum(n) FROM x`)
		done <- err
	}()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("slow query cancellation: %v", err)
		}
	}()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for query.db.Stats().InUse == 0 {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("SQL connection was not checked out")
		}
	}
	work, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := s.RegisterCollector(work, CollectorRecord{ID: "fake-concurrent", FirstSeen: 1, LastSeen: 1}); err != nil {
		t.Fatalf("SQL query blocked writer: %v", err)
	}
	if _, found, err := status.Collector(work, "fake-concurrent"); err != nil || !found {
		t.Fatalf("SQL query blocked status reader: %v", err)
	}
}
