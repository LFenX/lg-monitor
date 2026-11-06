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
