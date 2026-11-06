CREATE TABLE collectors (id TEXT PRIMARY KEY, hostname TEXT, first_seen INTEGER, last_seen INTEGER, version TEXT);
CREATE TABLE target_status (collector_id TEXT, target_id TEXT, ts INTEGER, state TEXT, detail TEXT, PRIMARY KEY (collector_id, target_id, ts));
CREATE TABLE probe_results (collector_id TEXT, target_id TEXT, probe TEXT, ts INTEGER, ok INTEGER, latency_ms INTEGER, error TEXT);
CREATE TABLE process_samples (collector_id TEXT, target_id TEXT, ts INTEGER, pid INTEGER, cpu REAL, rss INTEGER);
CREATE TABLE alerts (id TEXT PRIMARY KEY, rule_id TEXT, collector_id TEXT, target_id TEXT, state TEXT, severity TEXT,
                     started_at INTEGER, resolved_at INTEGER, last_notified_at INTEGER, summary TEXT);
CREATE TABLE alert_events (alert_id TEXT, ts INTEGER, kind TEXT, message TEXT);
CREATE TABLE notifications (id TEXT PRIMARY KEY, alert_id TEXT, notifier_id TEXT, template TEXT, ts INTEGER,
                            ok INTEGER, error TEXT, message_id TEXT);
CREATE INDEX probe_results_target_time ON probe_results(collector_id, target_id, ts);
CREATE INDEX process_samples_target_time ON process_samples(collector_id, target_id, ts);
CREATE INDEX target_status_time ON target_status(ts);
CREATE INDEX probe_results_time ON probe_results(ts);
CREATE INDEX process_samples_time ON process_samples(ts);
CREATE INDEX alerts_target_time ON alerts(collector_id, target_id, started_at);
CREATE INDEX alerts_state ON alerts(state);
CREATE INDEX alert_events_time ON alert_events(alert_id, ts);
CREATE INDEX notifications_time ON notifications(alert_id, ts);
