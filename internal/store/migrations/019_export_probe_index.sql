-- Probe periods can differ greatly; latest-per-probe reads must not walk other probe histories.
CREATE INDEX probe_results_export_latest ON probe_results(collector_id,target_id,probe,ts);
