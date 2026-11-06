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
