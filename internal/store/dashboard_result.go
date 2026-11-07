package store

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/LFenX/lg-monitor/internal/rules"
)

func ValidatePanelResult(kind string, result rules.QueryResult) error {
	required := map[string][]string{"stat": {"value"}, "line": {"ts", "value"}, "bar": {"label", "value"}, "status_grid": {"label", "state"}, "table": {}, "alerts": {"summary", "severity", "state"}, "log": {"ts", "message"}}
	columns, known := required[kind]
	if !known {
		return fmt.Errorf("不支持的面板类型 %s", kind)
	}
	for _, column := range columns {
		if !slices.Contains(result.Columns, column) {
			return fmt.Errorf("面板 %s 要求列 %s；实际返回：%s", kind, strings.Join(columns, "、"), strings.Join(result.Columns, "、"))
		}
	}
	seen := map[string]bool{}
	for _, column := range result.Columns {
		if column == "" || seen[column] {
			return fmt.Errorf("查询列名不能为空或重复；实际返回：%s", strings.Join(result.Columns, "、"))
		}
		seen[column] = true
	}
	if len(result.Columns) == 0 {
		return fmt.Errorf("查询没有返回有效列")
	}
	if kind == "stat" && len(result.Rows) > 1 {
		return fmt.Errorf("stat 查询只能返回一行，请使用聚合或 LIMIT 1")
	}
	value := func(row []any, name string) any { return row[slices.Index(result.Columns, name)] }
	isNumber := func(value any) bool {
		switch v := value.(type) {
		case int64:
			return true
		case float64:
			return !math.IsInf(v, 0) && !math.IsNaN(v)
		}
		return false
	}
	isText := func(value any) bool { _, ok := value.(string); return ok }
	for index, row := range result.Rows {
		fail := func(message string) error { return fmt.Errorf("第 %d 行：%s", index+1, message) }
		if len(row) != len(result.Columns) {
			return fail("值数量与查询列数量不一致")
		}
		if kind == "stat" || kind == "line" || kind == "bar" {
			v := value(row, "value")
			if v != nil && !isNumber(v) && !(kind == "stat" && isText(v)) {
				return fail("value 必须为有限数值或 null；stat 也可以是文字")
			}
		}
		if kind == "line" || kind == "log" {
			var ts float64
			switch v := value(row, "ts").(type) {
			case int64:
				ts = float64(v)
			case float64:
				ts = v
			default:
				return fail("ts 必须是 UTC Unix 毫秒整数")
			}
			if math.IsInf(ts, 0) || math.IsNaN(ts) || math.Trunc(ts) != ts || math.Abs(ts) > 8640000000000000 {
				return fail("ts 必须是有效的 UTC Unix 毫秒整数")
			}
		}
		if (kind == "bar" || kind == "status_grid") && !isText(value(row, "label")) {
			return fail("label 必须是文字")
		}
		if kind == "line" && slices.Contains(result.Columns, "series") && value(row, "series") != nil && !isText(value(row, "series")) {
			return fail("series 必须是文字或 null")
		}
		if kind == "status_grid" && !slices.Contains([]any{"up", "down", "unknown"}, value(row, "state")) {
			return fail("state 必须是 up、down 或 unknown")
		}
		if kind == "alerts" {
			if !isText(value(row, "summary")) {
				return fail("summary 必须是文字")
			}
			if !slices.Contains([]any{"critical", "warning", "info"}, value(row, "severity")) {
				return fail("severity 必须是 critical、warning 或 info")
			}
			if !slices.Contains([]any{"pending", "firing", "resolved"}, value(row, "state")) {
				return fail("state 必须是 pending、firing 或 resolved")
			}
		}
		if kind == "log" && !isText(value(row, "message")) {
			return fail("message 必须是文字")
		}
	}
	return nil
}
