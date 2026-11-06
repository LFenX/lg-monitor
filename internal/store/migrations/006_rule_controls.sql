-- @column alerts.instance_key SQL row instance key, empty for built-in single-instance rules.
-- @column alerts.acknowledged_at First acknowledgement time in UTC Unix milliseconds; zero if unacknowledged.
-- @column alerts.notification_at Latest notification transition time in UTC Unix milliseconds.
-- @column alerts.notification_key Stable firing, recovery, or reminder event identity.
-- @column alert_events.event_key,notifications.event_key Stable notification event identity used for deduplication.
-- @column process_samples.created_at Process creation time in UTC Unix milliseconds, paired with PID.
-- @table silences Rule or target notification maintenance windows.
-- @column silences.id Unique silence ID returned by CLI and API.
-- @column silences.rule_id Selected rule ID, or empty when targeting a target.
-- @column silences.target_id Selected target ID, or empty when targeting a rule.
-- @column silences.starts_at,silences.ends_at Silence start or end time in UTC Unix milliseconds.
-- @column silences.reason User-provided maintenance reason.
-- @table alert_groups Persisted flapping and digest decisions per rule instance.
-- @column alert_groups.rule_id Stable configured rule ID.
-- @column alert_groups.collector_id Stable collector ID.
-- @column alert_groups.target_id Stable target ID.
-- @column alert_groups.instance_key SQL instance key, empty for built-in rules.
-- @column alert_groups.changed_at Last lifecycle change time in UTC Unix milliseconds.
-- @column alert_groups.flapping One while repeated transitions are being merged into a digest.
-- @column alert_groups.digest_alert Alert selected to carry the current digest.
-- @column alert_groups.digest_key Notification event selected to carry the current digest.
-- @table notification_events Durable per-event suppression and digest decisions.
-- @column notification_events.alert_id Stable referenced alert ID.
-- @column notification_events.event_key Stable notification event identity.
-- @column notification_events.state Alert state associated with the event.
-- @column notification_events.ts Notification decision time in UTC Unix milliseconds.
-- @column notification_events.suppressed Empty if eligible; otherwise silenced, acknowledged, or flapping reason.
-- @column notification_events.digest One when this event carries a merged digest.
-- @table rule_errors Latest persisted SQL rule evaluation errors.
-- @column rule_errors.rule_id Stable configured rule ID.
-- @column rule_errors.last_error User-facing latest rule evaluation error.
-- @column rule_errors.error_at Error observation time in UTC Unix milliseconds.
-- @column rule_errors.consecutive Consecutive failed evaluation count.
-- @column rule_errors.active One while the latest failure remains active, zero after a successful evaluation.

ALTER TABLE alerts ADD COLUMN instance_key TEXT NOT NULL DEFAULT '';
ALTER TABLE alerts ADD COLUMN acknowledged_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE alerts ADD COLUMN notification_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE alerts ADD COLUMN notification_key TEXT NOT NULL DEFAULT '';
DROP INDEX alerts_one_firing;
CREATE UNIQUE INDEX alerts_one_firing ON alerts(rule_id,collector_id,target_id,instance_key) WHERE state='firing';
ALTER TABLE alert_events ADD COLUMN event_key TEXT NOT NULL DEFAULT '';
ALTER TABLE notifications ADD COLUMN event_key TEXT NOT NULL DEFAULT '';
CREATE TABLE silences (id TEXT PRIMARY KEY, rule_id TEXT NOT NULL DEFAULT '', target_id TEXT NOT NULL DEFAULT '',
                        starts_at INTEGER NOT NULL, ends_at INTEGER NOT NULL, reason TEXT NOT NULL);
CREATE INDEX silences_time ON silences(ends_at);
CREATE TABLE alert_groups (rule_id TEXT, collector_id TEXT, target_id TEXT, instance_key TEXT,
                          changed_at INTEGER NOT NULL, flapping INTEGER NOT NULL DEFAULT 0,
                          digest_alert TEXT NOT NULL DEFAULT '', digest_key TEXT NOT NULL DEFAULT '',
                          PRIMARY KEY(rule_id,collector_id,target_id,instance_key));
CREATE TABLE notification_events (alert_id TEXT, event_key TEXT, state TEXT NOT NULL, ts INTEGER NOT NULL,
                                  suppressed TEXT NOT NULL DEFAULT '', digest INTEGER NOT NULL DEFAULT 0,
                                  PRIMARY KEY(alert_id,event_key));
CREATE INDEX alert_events_time_kind ON alert_events(ts,kind);
ALTER TABLE process_samples ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0;
CREATE TABLE rule_errors (rule_id TEXT PRIMARY KEY, last_error TEXT NOT NULL, error_at INTEGER NOT NULL,
                           consecutive INTEGER NOT NULL, active INTEGER NOT NULL);
