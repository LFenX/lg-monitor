package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
)

const QueryLimit = 10000
const QueryBudget = 5 * time.Second

func (v *View) Query(parent context.Context, query string) (rules.QueryResult, error) {
	return queryReadOnly(parent, v.db, query, QueryLimit, sql.Named("now", v.now().UnixMilli()))
}

func queryReadOnly(parent context.Context, db *sql.DB, query string, limit int, args ...any) (rules.QueryResult, error) {
	result := rules.QueryResult{Columns: []string{}, Rows: [][]any{}}
	if err := config.ValidateReadOnlySQL(query); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(parent, QueryBudget)
	defer cancel()
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return result, readQueryError(ctx, "执行只读 SQL 失败", err)
	}
	defer rows.Close()
	result.Columns, err = rows.Columns()
	if err != nil {
		return result, fmt.Errorf("读取 SQL 列名失败：%w", err)
	}
	for rows.Next() {
		if len(result.Rows) == limit {
			result.Truncated = true
			break
		}
		values, destinations := make([]any, len(result.Columns)), make([]any, len(result.Columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return result, fmt.Errorf("读取 SQL 结果失败：%w", err)
		}
		for i, value := range values {
			if number, ok := value.(float64); ok && (math.IsInf(number, 0) || math.IsNaN(number)) {
				return result, fmt.Errorf("SQL 第 %d 行第 %d 列返回非有限数值，请使用 CASE 或 CAST 转为可表示的值", len(result.Rows)+1, i+1)
			}
		}
		result.Rows = append(result.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return result, readQueryError(ctx, "遍历 SQL 结果失败", err)
	}
	return result, nil
}

func readQueryError(ctx context.Context, message string, err error) error {
	if failure := ctx.Err(); failure != nil {
		if errors.Is(failure, context.DeadlineExceeded) {
			return fmt.Errorf("只读 SQL 查询达到时间上限（最多 5 秒），请缩小查询：%w", errors.Join(failure, err))
		}
		return fmt.Errorf("只读 SQL 查询已取消：%w", errors.Join(failure, err))
	}
	return fmt.Errorf("%s：%w", message, err)
}
