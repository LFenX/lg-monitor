-- @table job_events Every business event a batch job reported, kept whole; daily records are derived from all of them, not from a last state.
-- @column job_events.job_id Batch job target ID.
-- @column job_events.event_id Reporter's stable event ID; a repeated ID is a duplicate and is not stored again.
-- @column job_events.business_date Business date (Beijing time, YYYY-MM-DD) of the run the event belongs to.
-- @column job_events.attempt_id Run the event belongs to; a manual rerun has its own attempt ID.
-- @column job_events.ts When the event happened, in UTC Unix milliseconds as reported.
-- @column job_events.received_at When lgmon stored the event, in UTC Unix milliseconds.
-- @column job_events.source Where it came from: api, or collector:<collector ID> for a JSON Lines file.
-- @column job_events.body The checked and redacted event as JSON (states, counts, summaries and receipts only).
CREATE TABLE job_events (
    job_id        TEXT NOT NULL,
    event_id      TEXT NOT NULL,
    business_date TEXT NOT NULL,
    attempt_id    TEXT NOT NULL,
    ts            INTEGER NOT NULL,
    received_at   INTEGER NOT NULL,
    source        TEXT NOT NULL,
    body          TEXT NOT NULL,
    PRIMARY KEY (job_id, event_id)
) WITHOUT ROWID;
CREATE INDEX job_events_day ON job_events(job_id, business_date);
CREATE INDEX job_events_attempt ON job_events(job_id, attempt_id);
