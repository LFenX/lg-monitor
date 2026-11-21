// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package blueprint

// TaskRuleQuery fires while the newest recorded state of a task monitor is down. The monitor
// records task states every minute from the collector's task inventory, so the alert and the
// page always agree; a state older than ten minutes says nothing and does not fire.
func TaskRuleQuery(target string) string {
	quoted := "'" + target + "'"
	return `SELECT t.collector_id AS key, json_extract(t.detail, '$.reason') AS summary
FROM task_status t
WHERE t.target_id = ` + quoted + ` AND t.state = 'down' AND t.ts >= :now - 600000
  AND t.ts = (SELECT max(n.ts) FROM task_status n WHERE n.collector_id = t.collector_id AND n.target_id = t.target_id AND n.ts >= :now - 600000)`
}
