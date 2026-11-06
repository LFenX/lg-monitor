CREATE UNIQUE INDEX alerts_one_firing ON alerts(rule_id, collector_id, target_id) WHERE state = 'firing';
