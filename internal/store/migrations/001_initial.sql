-- @table schema_migrations Applied monitor schema migrations.
-- @column schema_migrations.version Applied migration number.
-- @column schema_migrations.applied_at Application time in UTC Unix milliseconds.
-- @table collectors Stable collector identities and monitor-side heartbeat receipt times.
-- @column collectors.id Stable collector ID.
-- @column collectors.hostname Last reported hostname.
-- @column collectors.first_seen First registration time in UTC Unix milliseconds.
-- @column collectors.last_seen Last heartbeat receipt time in UTC Unix milliseconds; not a historical heartbeat series.
-- @column collectors.version Last reported collector program version.
-- @table target_status Raw target state snapshots retained according to the raw policy.
-- @column target_status.collector_id,probe_results.collector_id,process_samples.collector_id,alerts.collector_id Stable collector ID.
-- @column target_status.target_id,probe_results.target_id,process_samples.target_id,alerts.target_id Stable configured target ID; collector alerts use @collector.
-- @column target_status.ts,probe_results.ts,process_samples.ts Sample time in UTC Unix milliseconds.
-- @column target_status.state Last observed up, down, or unknown state.
-- @column target_status.detail JSON target facts, process references, ports, and diagnostic reason; excludes command lines.
-- @table probe_results Raw HTTP, TCP, and script probe results.
-- @column probe_results.probe Probe index from the target configuration, stored as text.
-- @column probe_results.ok One for success, zero for failure.
-- @column probe_results.latency_ms Probe elapsed time in milliseconds.
-- @column probe_results.error User-facing probe failure diagnostic.
-- @table process_samples Raw matched process CPU and resident memory samples.
-- @column process_samples.pid Windows process ID; combine with created_at to avoid PID reuse.
-- @column process_samples.cpu CPU percent normalized to the whole machine, from zero to one hundred per process.
-- @column process_samples.rss Resident memory in bytes.
-- @table alerts Durable alert lifecycle and acknowledgement state.
-- @column alerts.id Stable alert ID shared by firing, recovery, and notification attempts.
-- @column alerts.rule_id Stable configured rule ID.
-- @column alerts.state Firing or resolved lifecycle state.
-- @column alerts.severity Critical, warning, or info severity.
-- @column alerts.started_at Firing start time in UTC Unix milliseconds.
-- @column alerts.resolved_at Recovery time in UTC Unix milliseconds; null or zero while firing.
-- @column alerts.last_notified_at Last recorded successful notification time in UTC Unix milliseconds.
-- @column alerts.summary User-facing alert summary.
-- @table alert_events Durable alert transitions and reminder events.
-- @column alert_events.alert_id,notifications.alert_id Referenced alert ID.
-- @column alert_events.ts,notifications.ts Event or attempt time in UTC Unix milliseconds.
-- @column alert_events.kind Firing, resolved, renotify, or closed event kind.
-- @column alert_events.message User-facing transition summary.
-- @table notifications Audited notification delivery attempts and successful receipts.
-- @column notifications.id Unique delivery attempt ID.
-- @column notifications.notifier_id Stable configured notification channel ID.
-- @column notifications.template Effective card template ID after any fallback.
-- @column notifications.ok One for a recorded successful send, zero for a failed attempt.
-- @column notifications.error Sanitized user-facing delivery failure reason.
-- @column notifications.message_id Provider message ID after a successful send.

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
