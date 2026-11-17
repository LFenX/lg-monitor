-- @table job_sources How each batch job's JSON Lines file is being read: by which collector, whether it reads, and the last problem with it.
-- @column job_sources.job_id Batch job target ID.
-- @column job_sources.collector_id Collector that reads the file.
-- @column job_sources.path File path on that collector's computer.
-- @column job_sources.state ok while the collector can read the file; error after it could not, until it reads again.
-- @column job_sources.problem Last problem: the file could not be read, or a line was not a usable event; empty when none yet.
-- @column job_sources.problem_line Line the last problem was on; 0 for the file itself.
-- @column job_sources.problem_at When the last problem was reported, in UTC Unix milliseconds; 0 when none yet.
-- @column job_sources.updated_at When the collector last reported on the file, in UTC Unix milliseconds.
-- @column job_sources.last_event_at When a new event from the file was last stored, in UTC Unix milliseconds; 0 before the first.
CREATE TABLE job_sources (
    job_id        TEXT PRIMARY KEY,
    collector_id  TEXT NOT NULL,
    path          TEXT NOT NULL,
    state         TEXT NOT NULL CHECK (state IN ('ok', 'error')),
    problem       TEXT NOT NULL,
    problem_line  INTEGER NOT NULL,
    problem_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL,
    last_event_at INTEGER NOT NULL
) WITHOUT ROWID;
