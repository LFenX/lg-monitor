ALTER TABLE notifications ADD COLUMN state TEXT NOT NULL DEFAULT '';
CREATE INDEX notifications_delivery ON notifications(alert_id,state,notifier_id,ok,ts);
