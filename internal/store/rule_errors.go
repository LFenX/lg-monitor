package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/rules"
)

func (s *Store) RecordRuleEvaluation(ctx context.Context, status rules.RuleError) error {
	if status.RuleID == "" {
		return fmt.Errorf("规则状态缺少 ID")
	}
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if status.Error == "" {
			if _, err := tx.ExecContext(ctx, `UPDATE rule_errors SET active=0,consecutive=0 WHERE rule_id=?`, status.RuleID); err != nil {
				return fmt.Errorf("清除规则出错状态失败：%w", err)
			}
			return nil
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO rule_errors(rule_id,last_error,error_at,consecutive,active) VALUES(?,?,?,?,?) ON CONFLICT(rule_id) DO UPDATE SET last_error=excluded.last_error,error_at=excluded.error_at,consecutive=excluded.consecutive,active=excluded.active`, status.RuleID, status.Error, status.At, status.Consecutive, status.Active)
		if err != nil {
			return fmt.Errorf("保存规则最近错误失败：%w", err)
		}
		return nil
	})
}

func (v *View) RecentRuleErrors(ctx context.Context) ([]rules.RuleError, error) {
	rows, err := v.db.QueryContext(ctx, `SELECT rule_id,last_error,error_at,consecutive,active FROM rule_errors ORDER BY error_at DESC,rule_id`)
	if err != nil {
		return nil, fmt.Errorf("查询规则最近错误失败：%w", err)
	}
	defer rows.Close()
	result := []rules.RuleError{}
	for rows.Next() {
		var row rules.RuleError
		if err := rows.Scan(&row.RuleID, &row.Error, &row.At, &row.Consecutive, &row.Active); err != nil {
			return nil, fmt.Errorf("读取规则出错状态失败：%w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历规则出错状态失败：%w", err)
	}
	return result, nil
}
