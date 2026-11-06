ALTER TABLE collector_inbox RENAME TO collector_inbox_single;
-- @column collector_inbox.skipped One when the sequence was durably accepted without a primary sample, such as an expired late arrival.
CREATE TABLE collector_inbox (collector_id TEXT NOT NULL, seq INTEGER NOT NULL, payload TEXT NOT NULL,
                              skipped INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(collector_id,seq));
INSERT INTO collector_inbox(collector_id,seq,payload) SELECT collector_id,seq,payload FROM collector_inbox_single;
DROP TABLE collector_inbox_single;
