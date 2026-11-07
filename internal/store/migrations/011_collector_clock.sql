-- @column collectors.clock_offset_ms Estimated monitor time minus collector time in milliseconds; positive means the collector clock is behind.
-- @column collector_events.kind Data-gap, late_dropped, clock_skew or clock_synced event kind.
ALTER TABLE collectors ADD COLUMN clock_offset_ms INTEGER NOT NULL DEFAULT 0;
