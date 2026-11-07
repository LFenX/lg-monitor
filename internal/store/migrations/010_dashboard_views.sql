-- @table v_target_current Latest observed status for each collector and target.
-- @column v_target_current.collector_id Stable collector ID.
-- @column v_target_current.target_id Configured target ID.
-- @column v_target_current.ts Latest observation time in UTC Unix milliseconds.
-- @column v_target_current.state Latest observed up, down, or unknown state.
-- @column v_target_current.detail JSON observation details.
-- @table v_alerts_active Currently firing alerts; resolved and closed alerts are excluded.
-- @column v_alerts_active.id Alert instance ID.
-- @column v_alerts_active.rule_id Rule ID.
-- @column v_alerts_active.collector_id Collector ID.
-- @column v_alerts_active.target_id Target ID.
-- @column v_alerts_active.state Alert state, always firing in this view.
-- @column v_alerts_active.severity Alert critical, warning, or info severity.
-- @column v_alerts_active.started_at Alert start time in UTC Unix milliseconds.
-- @column v_alerts_active.resolved_at Resolution time in UTC Unix milliseconds, if any.
-- @column v_alerts_active.last_notified_at Last notification time in UTC Unix milliseconds.
-- @column v_alerts_active.summary Alert summary.
-- @table v_collectors Registered collectors and their latest persisted heartbeat, independent of live connection state.
-- @column v_collectors.collector_id Stable collector ID.
-- @column v_collectors.hostname Last reported hostname.
-- @column v_collectors.first_seen First registration time in UTC Unix milliseconds.
-- @column v_collectors.last_seen Last persisted heartbeat in UTC Unix milliseconds.
-- @column v_collectors.version Last reported collector version.

CREATE VIEW v_target_current AS
SELECT t.collector_id,t.target_id,t.ts,t.state,t.detail
FROM target_status t JOIN (
  SELECT collector_id,target_id,max(ts) AS ts
  FROM target_status GROUP BY collector_id,target_id
) latest USING (collector_id,target_id,ts);
CREATE VIEW v_alerts_active AS
SELECT id,rule_id,collector_id,target_id,state,severity,started_at,resolved_at,last_notified_at,summary
FROM alerts WHERE state='firing';
CREATE VIEW v_collectors AS
SELECT id AS collector_id,hostname,first_seen,last_seen,version FROM collectors;
