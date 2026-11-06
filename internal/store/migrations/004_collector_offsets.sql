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
