CREATE INDEX IF NOT EXISTS target_status_history_cover ON target_status(collector_id, target_id, ts, state);
CREATE INDEX IF NOT EXISTS task_status_history_cover ON task_status(collector_id, target_id, ts, state);
CREATE INDEX IF NOT EXISTS probe_results_history_cover ON probe_results(collector_id, target_id, ts, ok, latency_ms);
