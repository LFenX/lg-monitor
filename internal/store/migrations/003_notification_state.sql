ALTER TABLE notifications ADD COLUMN state TEXT NOT NULL DEFAULT '';
-- @column notifications.state Alert state sent by this attempt, such as firing or resolved.
CREATE INDEX notifications_delivery ON notifications(alert_id,state,notifier_id,ok,ts);
