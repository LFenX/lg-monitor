package store

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/LFenX/lg-monitor/internal/rules"
)

type AlertDigest struct {
	Alerts []rules.Alert
	Total  int
}

func (s *Store) DigestAlerts(ctx context.Context, current rules.Alert, through int64) (AlertDigest, error) {
	if through <= 0 {
		return AlertDigest{}, fmt.Errorf("汇总告警需要明确截止时间")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,state,severity,started_at,coalesce(resolved_at,0),summary,count(*) OVER()
FROM alerts WHERE rule_id=? AND collector_id=? AND target_id=? AND instance_key=? AND started_at>=? AND started_at<=?
ORDER BY started_at DESC,id DESC LIMIT 20`, current.RuleID, current.CollectorID, current.TargetID, current.InstanceKey, through-(10*time.Minute).Milliseconds(), through)
	if err != nil {
		return AlertDigest{}, fmt.Errorf("查询同组告警汇总失败：%w", err)
	}
	defer rows.Close()
	result := AlertDigest{Alerts: []rules.Alert{}}
	for rows.Next() {
		alert := rules.Alert{RuleID: current.RuleID, CollectorID: current.CollectorID, TargetID: current.TargetID, InstanceKey: current.InstanceKey}
		if err := rows.Scan(&alert.ID, &alert.State, &alert.Severity, &alert.StartedAt, &alert.ResolvedAt, &alert.Summary, &result.Total); err != nil {
			return AlertDigest{}, fmt.Errorf("读取同组告警汇总失败：%w", err)
		}
		result.Alerts = append(result.Alerts, alert)
	}
	if err := rows.Err(); err != nil {
		return AlertDigest{}, fmt.Errorf("遍历同组告警汇总失败：%w", err)
	}
	slices.Reverse(result.Alerts)
	return result, nil
}
