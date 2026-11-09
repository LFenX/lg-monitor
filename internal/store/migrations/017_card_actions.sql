-- @column silences.collector_id Collector scope for a Feishu card action; empty for manual rule/target windows.
-- @column silences.instance_key Exact SQL instance scope for a Feishu card action; may be empty for built-in rules.
-- @column silences.origin feishu_card for exact rule/collector/target/instance windows; empty for existing manual windows.
-- @column silences.scope_rule_id Exact rule for a Feishu card action; existing rule_id/target_id selection semantics stay unchanged.
-- @table card_action_events Committed Feishu card operations and durable duplicate-event results; contains no raw callbacks or signing tokens.
-- @column card_action_events.app_id Configured application alias, not a credential.
-- @column card_action_events.event_id Platform callback event identity, unique within the application.
-- @column card_action_events.notification_id Successful notification audit record used to bind the originating message.
-- @column card_action_events.alert_id Referenced firing alert lifecycle.
-- @column card_action_events.collector_id Stable collector ID.
-- @column card_action_events.target_id Stable target ID.
-- @column card_action_events.rule_id Stable rule ID.
-- @column card_action_events.instance_key Exact rule instance key, empty for built-in rules.
-- @column card_action_events.operator_hash Monitor-local HMAC digest of the callback operator identity; no raw user identifier.
-- @column card_action_events.operation ack or silence_1h.
-- @column card_action_events.ts Commit time in UTC Unix milliseconds; event-time SQL replay excludes future operations.
-- @column card_action_events.silence_id Exact group silence ID, empty for acknowledgements.
-- @column card_action_events.silence_until Original committed silence end in UTC Unix milliseconds; duplicates never extend it.
-- @column card_action_events.acknowledged_at Original first acknowledgement time in UTC Unix milliseconds.
ALTER TABLE silences ADD COLUMN collector_id TEXT NOT NULL DEFAULT '';
ALTER TABLE silences ADD COLUMN instance_key TEXT NOT NULL DEFAULT '';
ALTER TABLE silences ADD COLUMN origin TEXT NOT NULL DEFAULT '';
ALTER TABLE silences ADD COLUMN scope_rule_id TEXT NOT NULL DEFAULT '';
CREATE INDEX silences_card_scope ON silences(scope_rule_id,collector_id,target_id,instance_key,ends_at) WHERE origin='feishu_card';
CREATE TABLE card_action_events (
 app_id TEXT NOT NULL,event_id TEXT NOT NULL,notification_id TEXT NOT NULL,alert_id TEXT NOT NULL,
 collector_id TEXT NOT NULL,target_id TEXT NOT NULL,rule_id TEXT NOT NULL,instance_key TEXT NOT NULL,
 operator_hash TEXT NOT NULL,operation TEXT NOT NULL,ts INTEGER NOT NULL,
 silence_id TEXT NOT NULL DEFAULT '',silence_until INTEGER NOT NULL DEFAULT 0,acknowledged_at INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(app_id,event_id)
);
CREATE INDEX card_action_events_time ON card_action_events(ts);
