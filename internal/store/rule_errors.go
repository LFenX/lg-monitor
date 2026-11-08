package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/LFenX/lg-monitor/internal/rules"
)

func (s *Store) RecordRuleEvaluation(ctx context.Context, status rules.RuleError) error {
	if status.RuleID == "" {
		return fmt.Errorf("规则状态缺少 ID")
	}
	err := s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
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
	if err == nil {
		s.signalRuleErrors()
	}
	return err
}

func (s *Store) RuleErrorChanges() <-chan struct{} { return s.ruleErrorChanges }

func (s *Store) signalRuleErrors() {
	select {
	case s.ruleErrorChanges <- struct{}{}:
	default:
	}
}

func (s *Store) PruneRuleErrors(ctx context.Context, keep []string) error {
	removed := false
	err := s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		query := "DELETE FROM rule_errors"
		args := make([]any, len(keep))
		for index, id := range keep {
			args[index] = id
		}
		if len(keep) != 0 {
			query += " WHERE rule_id NOT IN (" + strings.TrimSuffix(strings.Repeat("?,", len(keep)), ",") + ")"
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("清理已删除规则的错误失败：%w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("读取规则错误清理结果失败：%w", err)
		}
		removed = count != 0
		return nil
	})
	if err == nil && removed {
		s.signalRuleErrors()
	}
	return err
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
