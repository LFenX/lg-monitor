package rules

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

type replayQueryFixture struct {
	query  ruleQueryFunc
	closed int
}

func (f *replayQueryFixture) LoadReplay(context.Context, config.Rule, int64, int64) (ReplayData, error) {
	return ReplayData{}, errors.New("SQL must use its own rule connection")
}
func (f *replayQueryFixture) OpenReplayQueries(context.Context) (ReplayQueryConnection, error) {
	return f, nil
}
func (f *replayQueryFixture) QueryRule(ctx context.Context, text string, at int64) (QueryResult, error) {
	return f.query(ctx, text, at)
}
func (f *replayQueryFixture) Close() error { f.closed++; return nil }

func sqlReplayOptions(f *replayQueryFixture) ReplayOptions {
	return ReplayOptions{Config: &config.Config{}, Rule: config.Rule{ID: "draft", Type: "sql", Query: "SELECT 'fake' AS key", Interval: "10s"}, Since: time.UnixMilli(1000000), Until: time.UnixMilli(1030000), Source: f}
}

func TestSQLReplayFirstErrorMiddleUnknownAndDeterministicEvents(t *testing.T) {
	f := &replayQueryFixture{query: func(context.Context, string, int64) (QueryResult, error) {
		return QueryResult{}, errors.New("fake first evaluation failure")
	}}
	if _, err := Replay(context.Background(), sqlReplayOptions(f)); err == nil || !strings.Contains(err.Error(), "fake first") {
		t.Fatalf("first error not returned: %v", err)
	}
	if f.closed != 1 {
		t.Fatal("SQL replay connection leaked")
	}
	f = &replayQueryFixture{query: func(_ context.Context, _ string, at int64) (QueryResult, error) {
		if at == 1010000 {
			return QueryResult{}, errors.New("fake middle failure")
		}
		rows := [][]any{{"fake"}}
		if at >= 1020000 {
			rows = nil
		}
		return QueryResult{Columns: []string{"key"}, Rows: rows}, nil
	}}
	result, err := Replay(context.Background(), sqlReplayOptions(f))
	if err != nil || !result.Complete || result.Evaluations != 4 || result.Errors.Count != 1 || result.Errors.First.TS != 1010000 || len(result.Events) != 2 || result.Events[0].TS != 1000000 || result.Events[1].TS != 1020000 || result.Totals.FiringAtEnd != 0 {
		t.Fatalf("SQL replay error/unknown behavior: %+v %v", result, err)
	}
}

func TestSQLReplayCancellationReturnsPartialAndRetentionIsClipped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &replayQueryFixture{}
	calls := 0
	f.query = func(ctx context.Context, _ string, _ int64) (QueryResult, error) {
		calls++
		if calls == 2 {
			cancel()
			return QueryResult{}, ctx.Err()
		}
		return QueryResult{Columns: []string{"key"}, Rows: [][]any{{"fake"}}}, nil
	}
	result, err := Replay(ctx, sqlReplayOptions(f))
	if err != nil || result.Complete || result.Evaluations != 2 || len(result.Events) != 1 || result.Totals.FiringAtEnd != 1 || f.closed != 1 {
		t.Fatalf("cancel did not return partial replay: %+v %v", result, err)
	}
	f = &replayQueryFixture{query: func(context.Context, string, int64) (QueryResult, error) {
		return QueryResult{Columns: []string{"key"}}, nil
	}}
	opts := sqlReplayOptions(f)
	opts.Rule.Interval = "24h"
	opts.Since = opts.Until.Add(-48 * time.Hour)
	result, err = Replay(context.Background(), opts)
	if err != nil || result.From != opts.Until.Add(-24*time.Hour).UnixMilli() || result.RequestedFrom != opts.Since.UnixMilli() || result.Evaluations != 2 || !result.Complete {
		t.Fatalf("retention clip: %+v %v", result, err)
	}
	if _, err := replayCount(0, 10000, 1); err == nil || !strings.Contains(err.Error(), "10000") {
		t.Fatalf("evaluation cap: %v", err)
	}
}

func TestSQLReplayDeadlineReturnsCalculatedEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	f := &replayQueryFixture{}
	calls := 0
	f.query = func(ctx context.Context, _ string, _ int64) (QueryResult, error) {
		calls++
		if calls == 2 {
			<-ctx.Done()
			return QueryResult{}, ctx.Err()
		}
		return QueryResult{Columns: []string{"key"}, Rows: [][]any{{"fake"}}}, nil
	}
	result, err := Replay(ctx, sqlReplayOptions(f))
	if err != nil || result.Complete || len(result.Events) != 1 || result.Evaluations != 2 || !strings.Contains(strings.Join(result.Notes, ""), "时限") || f.closed != 1 {
		t.Fatalf("deadline lost partial result: %+v %v", result, err)
	}
}
