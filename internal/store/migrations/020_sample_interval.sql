-- @column process_samples.interval_ms Configured target sampling period in milliseconds; legacy collectors used 2000ms.
ALTER TABLE process_samples ADD COLUMN interval_ms INTEGER NOT NULL DEFAULT 2000 CHECK(interval_ms BETWEEN 1 AND 86400000);
CREATE INDEX alerts_digest_group_time ON alerts(rule_id,collector_id,target_id,instance_key,started_at);
