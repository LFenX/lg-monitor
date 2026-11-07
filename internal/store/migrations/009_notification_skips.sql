-- @column notifications.skipped Whether this channel was deliberately skipped for the event; not a successful delivery and not replayed after restart.
ALTER TABLE notifications ADD COLUMN skipped INTEGER NOT NULL DEFAULT 0;
-- @column notifications.status_code HTTP status reported by a generic Webhook, zero when no response was received or this is another channel.
ALTER TABLE notifications ADD COLUMN status_code INTEGER NOT NULL DEFAULT 0;
