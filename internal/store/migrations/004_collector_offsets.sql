-- @table collector_offsets Durable transport acceptance and rule application watermarks.
-- @column collector_offsets.collector_id,collector_inbox.collector_id,collector_events.collector_id Stable collector ID.
-- @column collector_offsets.stored_seq Highest contiguous accepted transport sequence.
-- @column collector_offsets.applied_seq Highest contiguous sequence applied to rules; never exceeds stored_seq.
-- @table collector_inbox Accepted messages waiting for durable rule application.
-- @column collector_inbox.seq Transport message sequence.
-- @column collector_inbox.payload Serialized pipeline message JSON, or an empty payload for a skipped sequence.
-- @table collector_events Durable data-gap and late-sample loss audit.
-- @column collector_events.seq Positive transport gap sequence or independent negative late-drop audit sequence.
-- @column collector_events.ts Event audit time in UTC Unix milliseconds.
-- @column collector_events.kind Data-gap or late_dropped event kind.
-- @column collector_events.message Human-readable loss diagnostic.
-- @column collector_events.gap_from,collector_events.gap_to First and last lost transport sequence; zero for late-drop audits.
-- @column collector_events.dropped Number of lost or discarded records.

CREATE TABLE collector_offsets (
    collector_id TEXT PRIMARY KEY,
    stored_seq INTEGER NOT NULL DEFAULT 0,
    applied_seq INTEGER NOT NULL DEFAULT 0,
    CHECK(applied_seq >= 0 AND stored_seq >= applied_seq)
);
CREATE TABLE collector_inbox (
    collector_id TEXT PRIMARY KEY,
    seq INTEGER NOT NULL,
    payload TEXT NOT NULL
);
CREATE TABLE collector_events (
    collector_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    ts INTEGER NOT NULL,
    kind TEXT NOT NULL,
    message TEXT NOT NULL,
    gap_from INTEGER NOT NULL,
    gap_to INTEGER NOT NULL,
    dropped INTEGER NOT NULL,
    PRIMARY KEY(collector_id,seq)
);
