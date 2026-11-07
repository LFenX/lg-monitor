-- @table notification_pauses Global notification pause history and durable catch-up progress.
-- @column notification_pauses.id Stable pause identity used by catch-up event keys.
-- @column notification_pauses.started_at,notification_pauses.until Pause interval boundaries in UTC Unix milliseconds.
-- @column notification_pauses.ended_at Actual end time, null until expiry or explicit resume is processed.
-- @column notification_pauses.reason User-provided reason, at most 200 Unicode characters.
-- @column notification_pauses.by_name Name of the initiating token, or local.
-- @column notification_pauses.suppressed Number of distinct notification events suppressed by this pause.
-- @column notification_pauses.resumed_at Catch-up completion time, null while recovery remains outstanding.
-- @column notification_events.pause_id Pause which suppressed this event, empty for other decisions.
-- @column notification_events.resume_pause_id Pause whose durable catch-up event this is, otherwise empty.
-- @column notification_events.completed_at Catch-up delivery completion time, null until settled.
CREATE TABLE notification_pauses (
    id TEXT PRIMARY KEY,
    started_at INTEGER NOT NULL,
    until INTEGER NOT NULL,
    ended_at INTEGER,
    reason TEXT NOT NULL,
    by_name TEXT NOT NULL,
    suppressed INTEGER NOT NULL DEFAULT 0,
    resumed_at INTEGER
);
CREATE UNIQUE INDEX notification_pauses_active ON notification_pauses((1)) WHERE ended_at IS NULL;
ALTER TABLE notification_events ADD COLUMN pause_id TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_events ADD COLUMN resume_pause_id TEXT NOT NULL DEFAULT '';
ALTER TABLE notification_events ADD COLUMN completed_at INTEGER;
CREATE INDEX notification_events_pause ON notification_events(pause_id,alert_id,ts);
CREATE INDEX notification_events_resume ON notification_events(resume_pause_id,completed_at);
