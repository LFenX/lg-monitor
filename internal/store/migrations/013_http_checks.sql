-- @column probe_results.tls_not_after Earliest leaf or intermediate certificate expiry in UTC Unix milliseconds; NULL when not observed.
-- @column probe_results.tls_subject CN, or first DNS name, of that certificate; at most 200 Unicode characters.
-- @column alerts.cert_json Certificate snapshot for a cert_expiry alert, or JSON null.
-- @column alert_events.cert_json Certificate snapshot at this notification event, or JSON null; retained across renewal and restart.
-- @column alerts.instance_key,alert_groups.instance_key SQL row key or collector/target/probe certificate identity; empty for other built-in single-instance rules.
-- @table v_probe_certificates Latest certificate-bearing observation for each collector, target, and probe.
-- @column v_probe_certificates.collector_id Stable collector ID.
-- @column v_probe_certificates.target_id Configured target ID.
-- @column v_probe_certificates.probe Zero-based probe index stored as text.
-- @column v_probe_certificates.ts Certificate observation time in UTC Unix milliseconds.
-- @column v_probe_certificates.tls_not_after Earliest non-root certificate expiry in UTC Unix milliseconds.
-- @column v_probe_certificates.tls_subject Subject of the certificate selected by expiry.

ALTER TABLE probe_results ADD COLUMN tls_not_after INTEGER;
ALTER TABLE probe_results ADD COLUMN tls_subject TEXT NOT NULL DEFAULT '';
ALTER TABLE alerts ADD COLUMN cert_json TEXT NOT NULL DEFAULT 'null';
ALTER TABLE alert_events ADD COLUMN cert_json TEXT NOT NULL DEFAULT 'null';
CREATE INDEX probe_results_certificates ON probe_results(collector_id,target_id,probe,ts) WHERE tls_not_after IS NOT NULL;
CREATE VIEW v_probe_certificates AS
SELECT p.collector_id,p.target_id,p.probe,p.ts,p.tls_not_after,p.tls_subject
FROM probe_results p
WHERE p.tls_not_after IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM probe_results n
  WHERE n.collector_id=p.collector_id AND n.target_id=p.target_id AND n.probe=p.probe AND n.tls_not_after IS NOT NULL
    AND (n.ts>p.ts OR (n.ts=p.ts AND n.rowid>p.rowid))
);
