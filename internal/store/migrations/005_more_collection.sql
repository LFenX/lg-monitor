CREATE TABLE system_metrics (collector_id TEXT, ts INTEGER, cpu REAL, memory_total INTEGER, memory_used INTEGER,
                             memory_available INTEGER, memory_percent REAL, uptime INTEGER, detail TEXT,
                             PRIMARY KEY(collector_id,ts));
CREATE TABLE disk_metrics (collector_id TEXT, path TEXT COLLATE NOCASE, ts INTEGER, total INTEGER, used INTEGER, free INTEGER,
                           used_percent REAL, PRIMARY KEY(collector_id,path,ts));
CREATE TABLE network_metrics (collector_id TEXT, name TEXT, ts INTEGER, bytes_sent INTEGER, bytes_received INTEGER,
                              send_bytes_per_sec REAL, receive_bytes_per_sec REAL, PRIMARY KEY(collector_id,name,ts));
CREATE TABLE service_status (collector_id TEXT, name TEXT COLLATE NOCASE, ts INTEGER, state TEXT, start_type TEXT, pid INTEGER,
                             detail TEXT, PRIMARY KEY(collector_id,name,ts));
CREATE TABLE script_metrics (collector_id TEXT, target_id TEXT, probe TEXT, ts INTEGER, name TEXT, value REAL,
                             PRIMARY KEY(collector_id,target_id,probe,ts,name));
ALTER TABLE probe_results ADD COLUMN type TEXT NOT NULL DEFAULT '';
ALTER TABLE probe_results ADD COLUMN message TEXT NOT NULL DEFAULT '';
CREATE INDEX system_metrics_time ON system_metrics(ts);
CREATE INDEX disk_metrics_time ON disk_metrics(ts);
CREATE INDEX network_metrics_time ON network_metrics(ts);
CREATE INDEX service_status_time ON service_status(ts);
CREATE INDEX script_metrics_target_time ON script_metrics(collector_id,target_id,ts);
