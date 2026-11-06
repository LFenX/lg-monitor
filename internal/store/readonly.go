package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func (v *View) Query(parent context.Context, query string) (rules.QueryResult, error) {
	return queryReadOnly(parent, v.db, query, 10000, sql.Named("now", v.now().UnixMilli()))
}

func queryReadOnly(parent context.Context, db *sql.DB, query string, limit int, args ...any) (rules.QueryResult, error) {
	result := rules.QueryResult{Columns: []string{}, Rows: [][]any{}}
	if err := config.ValidateReadOnlySQL(query); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return result, fmt.Errorf("执行只读 SQL 失败：%w", err)
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
			if bytes, ok := value.([]byte); ok {
				values[i] = string(bytes)
			}
		}
		result.Rows = append(result.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("遍历 SQL 结果失败：%w", err)
	}
	return result, nil
}
