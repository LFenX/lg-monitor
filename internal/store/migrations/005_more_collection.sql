-- @table system_metrics Raw CPU, physical memory, and uptime samples.
-- @column system_metrics.collector_id,disk_metrics.collector_id,network_metrics.collector_id,service_status.collector_id,script_metrics.collector_id Stable collector ID.
-- @column system_metrics.ts,disk_metrics.ts,network_metrics.ts,service_status.ts,script_metrics.ts Sample time in UTC Unix milliseconds.
-- @column system_metrics.cpu Whole-machine CPU percent from zero to one hundred.
-- @column system_metrics.memory_total,system_metrics.memory_used,system_metrics.memory_available Physical memory in bytes, respectively total, used, and available.
-- @column system_metrics.memory_percent Used physical memory percent from zero to one hundred.
-- @column system_metrics.uptime System uptime in seconds.
-- @column system_metrics.detail JSON system sample including disk and network resources.
-- @table disk_metrics Raw disk capacity and usage by volume path.
-- @column disk_metrics.path Case-insensitive Windows volume path.
-- @column disk_metrics.total,disk_metrics.used,disk_metrics.free Disk capacity in bytes, respectively total, used, and free.
-- @column disk_metrics.used_percent Used disk capacity percent from zero to one hundred.
-- @table network_metrics Raw network counters and rates by interface.
-- @column network_metrics.name Network interface name.
-- @column network_metrics.bytes_sent,network_metrics.bytes_received Cumulative bytes sent or received on the interface.
-- @column network_metrics.send_bytes_per_sec,network_metrics.receive_bytes_per_sec Outbound or inbound rate in bytes per second.
-- @table service_status Raw Windows service changes, retaining the latest row per service after cleanup.
-- @column service_status.name Case-insensitive Windows service name.
-- @column service_status.state Observed service running, stopped, pending, paused, or unknown state.
-- @column service_status.start_type Configured Windows service startup type.
-- @column service_status.pid Service process ID, or zero if absent or unavailable.
-- @column service_status.detail JSON service facts and diagnostic details.
-- @table script_metrics Numeric custom metrics emitted by script probes.
-- @column script_metrics.target_id Stable configured target ID.
-- @column script_metrics.probe Probe index stored as text.
-- @column script_metrics.name Script-defined metric name; query and rollup APIs prefix it with script.
-- @column script_metrics.value Finite numeric metric value in script-defined units.
-- @column probe_results.type HTTP, TCP, or script probe type.
-- @column probe_results.message Script-provided status message, independent of the failure diagnostic.

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
