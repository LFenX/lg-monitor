package store

import (
	"math"
	"testing"

	"github.com/LFenX/lg-monitor/internal/rules"
)

func TestPanelResultContractsRejectMissingColumnsAndInvalidRows(t *testing.T) {
	for _, tc := range []struct {
		kind    string
		columns []string
		rows    [][]any
	}{
		{"line", []string{"time", "value"}, nil},
		{"line", []string{"ts", "value"}, [][]any{{"not-millis", int64(1)}}},
		{"bar", []string{"label", "value"}, [][]any{{"fake", math.Inf(1)}}},
		{"stat", []string{"value"}, [][]any{{int64(1)}, {int64(2)}}},
		{"status_grid", []string{"label", "state"}, [][]any{{"fake", "maybe"}}},
		{"alerts", []string{"summary", "severity", "state"}, [][]any{{"fake", "critical", "unknown"}}},
		{"log", []string{"ts", "message"}, [][]any{{int64(0), int64(1)}}},
		{"table", []string{"duplicate", "duplicate"}, [][]any{{int64(1), int64(2)}}},
	} {
		if err := ValidatePanelResult(tc.kind, rules.QueryResult{Columns: tc.columns, Rows: tc.rows}); err == nil {
			t.Fatalf("invalid %s result was accepted", tc.kind)
		}
	}
	for _, value := range []any{int64(0), nil, "虚构状态"} {
		if err := ValidatePanelResult("stat", rules.QueryResult{Columns: []string{"value"}, Rows: [][]any{{value}}}); err != nil {
			t.Fatal(err)
		}
	}
}
