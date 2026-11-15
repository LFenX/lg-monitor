-- @table task_status Scheduled task monitor state, recorded by the monitor every minute from the collector task inventory; retained with raw samples.
-- @column task_status.collector_id Stable collector ID.
-- @column task_status.target_id Monitor target ID that follows a scheduled task.
-- @column task_status.ts Observation time in UTC Unix milliseconds.
-- @column task_status.state up, down or unknown.
-- @column task_status.detail JSON with reason, enabled, lastRun, nextRun and lastResult; never a task action or credential.
-- @table status_hour Hourly target health recomputed by the monitor for recent hours; retained as long as hour summaries.
-- @column status_hour.collector_id Stable collector ID.
-- @column status_hour.target_id Stable target ID.
-- @column status_hour.ts UTC-aligned hour start in Unix milliseconds.
-- @column status_hour.up Samples in the hour whose state was up.
-- @column status_hour.total Samples whose state was up or down; unknown samples are not counted.
-- @column status_hour.worst Worst state seen in the hour: 0 down, 1 unknown, 2 up.
CREATE TABLE task_status (collector_id TEXT NOT NULL, target_id TEXT NOT NULL, ts INTEGER NOT NULL, state TEXT NOT NULL, detail TEXT NOT NULL,
                          PRIMARY KEY(collector_id, target_id, ts));
CREATE INDEX task_status_time ON task_status(ts);
CREATE TABLE status_hour (collector_id TEXT NOT NULL, target_id TEXT NOT NULL, ts INTEGER NOT NULL, up INTEGER NOT NULL, total INTEGER NOT NULL, worst INTEGER NOT NULL,
                          PRIMARY KEY(collector_id, target_id, ts));
CREATE INDEX status_hour_time ON status_hour(ts);
