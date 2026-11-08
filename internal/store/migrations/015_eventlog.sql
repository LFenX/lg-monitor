-- @table eventlog_events Windows Application/System event facts, deduplicated by source identity and time; retained with raw samples.
-- @column eventlog_events.collector_id,eventlog_matches.collector_id,v_eventlog_targets.collector_id Stable collector ID.
-- @column eventlog_events.channel,eventlog_matches.channel,v_eventlog_targets.channel Application or System log.
-- @column eventlog_events.record_id,eventlog_matches.record_id Windows record number; a cleared log can reuse it with a different timestamp.
-- @column eventlog_events.ts,eventlog_matches.ts,v_eventlog_targets.ts Event source time in UTC Unix milliseconds, before collector clock correction.
-- @column eventlog_events.provider,v_eventlog_targets.provider Windows event provider name.
-- @column eventlog_events.event_id,v_eventlog_targets.event_id Provider event ID.
-- @column eventlog_events.level,v_eventlog_targets.level Raw Windows event level, 0 through 255.
-- @column eventlog_events.subject,v_eventlog_targets.subject Program filename or service name for recognized crash presets; empty for other events.
-- @column eventlog_events.message,v_eventlog_targets.message Rendered event message or EventData fallback, at most 2 KiB of UTF-8.
-- @table eventlog_matches Frozen preset and target ownership, committed and deleted atomically with its event.
-- @column eventlog_matches.kind Match category: preset, subject, or subscription.
-- @column eventlog_matches.name Preset name or configured target ID, interpreted according to kind.
-- @table v_eventlog_targets One row per event and target; collector_id and target_id support scoped dashboard queries.
-- @column v_eventlog_targets.target_id Configured target ID at collection time.
-- @column v_eventlog_targets.via Frozen ownership: subject, subscription, or both.
-- @column alerts.source_ts Raw down observation time in UTC Unix milliseconds; zero for older alerts or rules without a down sample.
-- @column alerts.eventlog,alert_events.eventlog JSON event-rule count and most recent event; null for other rules. Alert events retain the original notification snapshot.
CREATE TABLE eventlog_events (
  collector_id TEXT NOT NULL,
  channel TEXT NOT NULL CHECK(channel IN ('Application','System')),
  record_id INTEGER NOT NULL,
  ts INTEGER NOT NULL,
  provider TEXT NOT NULL,
  event_id INTEGER NOT NULL,
  level INTEGER NOT NULL,
  subject TEXT NOT NULL,
  message TEXT NOT NULL,
  PRIMARY KEY(collector_id,channel,record_id,ts)
);
CREATE INDEX eventlog_events_time ON eventlog_events(ts);
CREATE INDEX eventlog_events_collector_time ON eventlog_events(collector_id,ts);
CREATE TABLE eventlog_matches (
  collector_id TEXT NOT NULL,
  channel TEXT NOT NULL,
  record_id INTEGER NOT NULL,
  ts INTEGER NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('preset','subject','subscription')),
  name TEXT NOT NULL,
  PRIMARY KEY(collector_id,channel,record_id,ts,kind,name),
  FOREIGN KEY(collector_id,channel,record_id,ts) REFERENCES eventlog_events(collector_id,channel,record_id,ts) ON DELETE CASCADE
);
CREATE INDEX eventlog_matches_target_time ON eventlog_matches(collector_id,kind,name,ts);
CREATE VIEW v_eventlog_targets AS
SELECT e.collector_id,m.name AS target_id,
CAST(CASE WHEN count(DISTINCT m.kind)=2 THEN 'both' ELSE min(m.kind) END AS TEXT) AS via,
e.ts,e.channel,e.provider,e.event_id,e.level,e.subject,e.message
FROM eventlog_events e JOIN eventlog_matches m USING(collector_id,channel,record_id,ts)
WHERE m.kind IN ('subject','subscription')
GROUP BY e.collector_id,e.channel,e.record_id,e.ts,m.name;
ALTER TABLE alerts ADD COLUMN source_ts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE alerts ADD COLUMN eventlog TEXT NOT NULL DEFAULT 'null';
ALTER TABLE alert_events ADD COLUMN eventlog TEXT NOT NULL DEFAULT 'null';
