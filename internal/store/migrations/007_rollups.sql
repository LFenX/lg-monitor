-- @table metric_minute Mergeable minute metric statistics, one row per metric and resource.
-- @table metric_hour Mergeable hour metric statistics composed from minute counts and sums.
-- @column metric_minute.source_table,metric_hour.source_table Raw source table used to isolate cleanup and recomputation.
-- @column metric_minute.collector_id,metric_hour.collector_id Stable collector ID.
-- @column metric_minute.target_id,metric_hour.target_id Target ID for process or script metrics, otherwise empty.
-- @column metric_minute.name,metric_hour.name Namespaced metric name such as system.cpu_percent or script.age.
-- @column metric_minute.resource,metric_hour.resource Disk path or network interface name; empty for other metrics.
-- @column metric_minute.probe,metric_hour.probe Script probe index as text, otherwise empty.
-- @column metric_minute.ts,metric_hour.ts UTC-aligned bucket start in Unix milliseconds.
-- @column metric_minute.samples,metric_hour.samples Number of source observations; process values are summed by target before counting.
-- @column metric_minute.sum_value,metric_hour.sum_value Sum of observed values; weighted average is sum_value divided by samples.
-- @column metric_minute.max_value,metric_hour.max_value Maximum observed value in the bucket.
-- @table probe_minute Mergeable minute probe success and latency statistics.
-- @table probe_hour Mergeable hour probe statistics composed from minute counts and sums.
-- @column probe_minute.collector_id,probe_hour.collector_id Stable collector ID.
-- @column probe_minute.target_id,probe_hour.target_id Stable target ID.
-- @column probe_minute.probe,probe_hour.probe Probe index stored as text.
-- @column probe_minute.ts,probe_hour.ts UTC-aligned bucket start in Unix milliseconds.
-- @column probe_minute.samples,probe_hour.samples Total probe attempt count.
-- @column probe_minute.successes,probe_hour.successes Successful attempt count; divide by samples for success rate.
-- @column probe_minute.latency_sum,probe_hour.latency_sum Sum of probe latency in milliseconds; divide by samples for mean latency.
-- @table rollup_dirty Internal versioned source buckets waiting for background recomputation.
-- @column rollup_dirty.source_table Raw source table for the bucket.
-- @column rollup_dirty.level Minute or hour aggregation level.
-- @column rollup_dirty.ts UTC-aligned bucket start in Unix milliseconds.
-- @column rollup_dirty.version Source revision used to reject stale aggregation snapshots.
-- @table rollup_state Internal bucket finalization records used to protect retained summaries.
-- @column rollup_state.source_table Raw source table associated with the summary.
-- @column rollup_state.level Minute or hour aggregation level.
-- @column rollup_state.ts UTC-aligned bucket start in Unix milliseconds.
-- @column rollup_state.finalized One when raw cleanup makes the bucket immutable to late samples.
-- @table raw_watermarks Monotone raw completeness boundaries used to audit discarded late samples.
-- @column raw_watermarks.table_name Raw source table name.
-- @column raw_watermarks.raw_complete_from Earliest complete raw timestamp in UTC Unix milliseconds; never moves backwards.
-- @column collector_events.source_table Raw source table affected by a late-drop event.
-- @column collector_events.first_ts,collector_events.last_ts First and last discarded sample time in UTC Unix milliseconds.

CREATE TABLE metric_minute (source_table TEXT NOT NULL, collector_id TEXT NOT NULL, target_id TEXT NOT NULL, name TEXT NOT NULL,
                            resource TEXT NOT NULL, probe TEXT NOT NULL, ts INTEGER NOT NULL,
                            samples INTEGER NOT NULL, sum_value REAL NOT NULL, max_value REAL NOT NULL,
                            PRIMARY KEY(collector_id,target_id,name,resource,probe,ts));
CREATE TABLE metric_hour (source_table TEXT NOT NULL, collector_id TEXT NOT NULL, target_id TEXT NOT NULL, name TEXT NOT NULL,
                          resource TEXT NOT NULL, probe TEXT NOT NULL, ts INTEGER NOT NULL,
                          samples INTEGER NOT NULL, sum_value REAL NOT NULL, max_value REAL NOT NULL,
                          PRIMARY KEY(collector_id,target_id,name,resource,probe,ts));
CREATE TABLE probe_minute (collector_id TEXT NOT NULL, target_id TEXT NOT NULL, probe TEXT NOT NULL, ts INTEGER NOT NULL,
                           samples INTEGER NOT NULL, successes INTEGER NOT NULL, latency_sum REAL NOT NULL,
                           PRIMARY KEY(collector_id,target_id,probe,ts));
CREATE TABLE probe_hour (collector_id TEXT NOT NULL, target_id TEXT NOT NULL, probe TEXT NOT NULL, ts INTEGER NOT NULL,
                         samples INTEGER NOT NULL, successes INTEGER NOT NULL, latency_sum REAL NOT NULL,
                         PRIMARY KEY(collector_id,target_id,probe,ts));
CREATE INDEX metric_minute_time ON metric_minute(ts);
CREATE INDEX metric_hour_time ON metric_hour(ts);
CREATE INDEX metric_minute_name_time ON metric_minute(collector_id,name,ts);
CREATE INDEX metric_hour_name_time ON metric_hour(collector_id,name,ts);
CREATE INDEX probe_minute_time ON probe_minute(ts);
CREATE INDEX probe_hour_time ON probe_hour(ts);
CREATE INDEX script_metrics_time ON script_metrics(ts);
CREATE INDEX metric_minute_source_time ON metric_minute(source_table,ts);
CREATE INDEX metric_hour_source_time ON metric_hour(source_table,ts);
CREATE TABLE rollup_dirty (source_table TEXT NOT NULL, level TEXT NOT NULL, ts INTEGER NOT NULL, version INTEGER NOT NULL,
                           PRIMARY KEY(source_table,level,ts));
CREATE INDEX rollup_dirty_level_time ON rollup_dirty(level,ts);
CREATE TABLE rollup_state (source_table TEXT NOT NULL, level TEXT NOT NULL, ts INTEGER NOT NULL, finalized INTEGER NOT NULL DEFAULT 0,
                           PRIMARY KEY(source_table,level,ts));
CREATE TABLE raw_watermarks (table_name TEXT PRIMARY KEY, raw_complete_from INTEGER NOT NULL);
ALTER TABLE collector_events ADD COLUMN source_table TEXT NOT NULL DEFAULT '';
ALTER TABLE collector_events ADD COLUMN first_ts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE collector_events ADD COLUMN last_ts INTEGER NOT NULL DEFAULT 0;
INSERT INTO rollup_dirty(source_table,level,ts,version)
 SELECT source_table,'minute', ts/60000*60000, 1 FROM (
   SELECT 'system_metrics' AS source_table,ts FROM system_metrics UNION ALL SELECT 'disk_metrics',ts FROM disk_metrics
   UNION ALL SELECT 'network_metrics',ts FROM network_metrics UNION ALL SELECT 'process_samples',ts FROM process_samples
   UNION ALL SELECT 'script_metrics',ts FROM script_metrics UNION ALL SELECT 'probe_results',ts FROM probe_results
 ) GROUP BY source_table,ts/60000*60000;
