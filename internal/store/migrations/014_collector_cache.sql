-- @column collectors.cache_bytes Last heartbeat's used SQLite pages in bytes; null for a collector without cache statistics.
-- @column collectors.cache_max_bytes Last heartbeat's effective cache limit including its WAL reserve, in bytes.
-- @column collectors.cache_bytes_per_hour Last heartbeat's estimated bytes per hour; null during warmup or without recent writes.
ALTER TABLE collectors ADD COLUMN cache_bytes INTEGER;
ALTER TABLE collectors ADD COLUMN cache_max_bytes INTEGER;
ALTER TABLE collectors ADD COLUMN cache_bytes_per_hour REAL;
