-- @table inventory_scopes Current read coverage and persistent baseline per collector and inventory source; retained without expiry.
-- @column inventory_scopes.collector_id,inventory_current.collector_id,inventory_snapshots.collector_id,inventory_changes.collector_id Stable collector ID.
-- @column inventory_scopes.kind,inventory_current.kind,inventory_snapshots.kind,inventory_changes.kind startup or tasks.
-- @column inventory_scopes.scope,inventory_current.scope,inventory_snapshots.scope,inventory_changes.scope Stable source identity; contains no command or user directory path.
-- @column inventory_scopes.seq,inventory_snapshots.seq,inventory_changes.seq Monotonic collector record sequence; duplicates and older snapshots are ignored.
-- @column inventory_scopes.ts,inventory_current.ts,inventory_snapshots.ts,inventory_changes.ts Observation time in UTC Unix milliseconds.
-- @column inventory_scopes.last_success Last successful complete source observation time; errors retain the old value.
-- @column inventory_scopes.epoch Public local HMAC key epoch; a change establishes a new startup baseline.
-- @column inventory_scopes.error_code Empty on success; otherwise read_failed, limit_exceeded, source_unavailable or unsupported.
-- @column inventory_scopes.initialized Whether this source has a successful baseline.
-- @table inventory_current Current complete startup entries and scheduled task facts; retained without expiry.
-- @column inventory_current.id,inventory_changes.id Stable case-insensitive entry identity within its source.
-- @column inventory_current.name,inventory_changes.name Display name; never a command line.
-- @column inventory_current.detail Whitelisted InventoryEntry JSON; no task action, credential, file content or raw startup registry value.
-- @table inventory_snapshots Successful full source snapshots for event-time SQL queries; retained with raw samples.
-- @column inventory_snapshots.entries JSON array of whitelisted InventoryEntry values at observation time.
-- @table inventory_changes Startup added/changed/removed observations after the first baseline; retained with raw samples.
-- @column inventory_changes.change added, changed or removed.
-- @column inventory_changes.old_fingerprint,inventory_changes.new_fingerprint Local HMAC-SHA256 fingerprints; empty when the corresponding side is absent.
CREATE TABLE inventory_scopes (
 collector_id TEXT NOT NULL,kind TEXT NOT NULL,scope TEXT NOT NULL,
 seq INTEGER NOT NULL,ts INTEGER NOT NULL,last_success INTEGER NOT NULL DEFAULT 0,
 epoch TEXT NOT NULL DEFAULT '',error_code TEXT NOT NULL DEFAULT '',initialized INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(collector_id,kind,scope)
);
CREATE TABLE inventory_current (
 collector_id TEXT NOT NULL,kind TEXT NOT NULL,scope TEXT NOT NULL,id TEXT NOT NULL,
 name TEXT NOT NULL,ts INTEGER NOT NULL,detail TEXT NOT NULL,
 PRIMARY KEY(collector_id,kind,scope,id)
);
CREATE TABLE inventory_snapshots (
 collector_id TEXT NOT NULL,kind TEXT NOT NULL,scope TEXT NOT NULL,seq INTEGER NOT NULL,
 ts INTEGER NOT NULL,entries TEXT NOT NULL,PRIMARY KEY(collector_id,seq)
);
CREATE INDEX inventory_snapshots_time ON inventory_snapshots(ts);
CREATE INDEX inventory_snapshots_source_time ON inventory_snapshots(collector_id,kind,scope,ts);
CREATE TABLE inventory_changes (
 collector_id TEXT NOT NULL,kind TEXT NOT NULL,scope TEXT NOT NULL,seq INTEGER NOT NULL,
 ts INTEGER NOT NULL,id TEXT NOT NULL,name TEXT NOT NULL,change TEXT NOT NULL,
 old_fingerprint TEXT NOT NULL,new_fingerprint TEXT NOT NULL,
 PRIMARY KEY(collector_id,seq,id)
);
CREATE INDEX inventory_changes_time ON inventory_changes(ts);
CREATE INDEX inventory_changes_collector_time ON inventory_changes(collector_id,ts);
