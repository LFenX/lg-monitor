package rules

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

type ruleQueryFunc func(context.Context, string, int64) (QueryResult, error)

func (f ruleQueryFunc) QueryRule(ctx context.Context, text string, at int64) (QueryResult, error) {
	return f(ctx, text, at)
}

func TestSQLQueryRunsOutsideEngineLockAndRetiredResultIsDiscarded(t *testing.T) {
	engine, _, queue, now := newAdditionalEngine(t, config.Rule{ID: "sql", Type: "sql", Query: "SELECT 1 AS key", Interval: "10s"}, nil)
	entered, release := make(chan int64, 1), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine.queries = ruleQueryFunc(func(ctx context.Context, _ string, at int64) (QueryResult, error) {
		entered <- at
		select {
		case <-release:
			return QueryResult{Columns: []string{"key"}, Rows: [][]any{{"fake"}}}, nil
		case <-ctx.Done():
			return QueryResult{}, ctx.Err()
		}
	})
	done := make(chan error, 1)
	go func() { done <- engine.Evaluate(ctx) }()
	select {
	case at := <-entered:
		if at != now.UnixMilli() {
			t.Fatal("SQL did not use evaluation clock")
		}
	case <-time.After(time.Second):
		t.Fatal("SQL did not start")
	}
	updated := make(chan error, 1)
	go func() { updated <- engine.UpdateConfig(&config.Config{}) }()
	select {
	case err := <-updated:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("query held the engine lock")
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("query did not finish")
	}
	if len(queue.alerts) != 0 {
		t.Fatal("retired SQL query published a stale alert")
	}
}

func TestSQLUnknownCancelsPendingAndRetainsRecentError(t *testing.T) {
	engine, _, queue, now := newAdditionalEngine(t, config.Rule{ID: "sql", Type: "sql", Query: "SELECT 1 AS key", Interval: "10s", For: "20s"}, nil)
	failed := false
	engine.queries = ruleQueryFunc(func(context.Context, string, int64) (QueryResult, error) {
		if failed {
			return QueryResult{}, errors.New("fake temporary SQL failure")
		}
		return QueryResult{Columns: []string{"key"}, Rows: [][]any{{"fake"}}}, nil
	})
	if err := engine.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(10 * time.Second)
	failed = true
	if err := engine.Evaluate(context.Background()); err == nil {
		t.Fatal("SQL failure not reported")
	}
	if states := engine.Snapshot(); len(states) != 1 || states[0].State != StateOK {
		t.Fatalf("unknown retained pending: %+v", states)
	}
	if rows := engine.RecentErrors(); len(rows) != 1 || !rows[0].Active || rows[0].Consecutive != 1 {
		t.Fatalf("SQL recent error: %+v", rows)
	}
	failed = false
	for range 2 {
		*now = now.Add(10 * time.Second)
		if err := engine.Evaluate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(queue.alerts) != 0 {
		t.Fatal("SQL failure did not restart the for duration")
	}
	*now = now.Add(10 * time.Second)
	if err := engine.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(queue.alerts) != 1 {
		t.Fatal("SQL condition did not fire after a new full duration")
	}
}
