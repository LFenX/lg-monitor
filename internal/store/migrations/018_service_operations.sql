-- @table operation_requests Durable administrator-confirmed remote service operation intents and outcomes; never an offline execution queue.
-- @column operation_requests.request_id Client-generated immutable lowercase hexadecimal request identity.
-- @column operation_requests.request_hash SHA-256 of the immutable collector, grant, policy version and reason.
-- @column operation_requests.collector_id Exact paired collector which owns the authorized service.
-- @column operation_requests.grant_id Collector-local grant identity; not a command or service argument.
-- @column operation_requests.policy_version SHA-256 of the reviewed collector-local policy.
-- @column operation_requests.service Exact SCM service name resolved by the collector policy.
-- @column operation_requests.actor Administrator token display name; never a bearer token.
-- @column operation_requests.reason Operator-provided audit reason; not executable input.
-- @column operation_requests.state running, succeeded, failed, denied or unknown; unfinished restart recovery never repeats execution.
-- @column operation_requests.started_at Monitor acceptance time in UTC Unix milliseconds.
-- @column operation_requests.finished_at Monitor outcome persistence time in UTC Unix milliseconds; zero while running.
-- @column operation_requests.collector_started_at Collector-reported service operation start, in its original UTC Unix milliseconds.
-- @column operation_requests.collector_finished_at Collector-reported finish, in its original UTC Unix milliseconds.
-- @column operation_requests.message Safe Chinese outcome summary; no raw operating-system errors or credentials.
CREATE TABLE operation_requests (
 request_id TEXT PRIMARY KEY,request_hash TEXT NOT NULL,collector_id TEXT NOT NULL,
 grant_id TEXT NOT NULL,policy_version TEXT NOT NULL,service TEXT NOT NULL,
 actor TEXT NOT NULL,reason TEXT NOT NULL,state TEXT NOT NULL,
 started_at INTEGER NOT NULL,finished_at INTEGER NOT NULL DEFAULT 0,
 collector_started_at INTEGER NOT NULL DEFAULT 0,collector_finished_at INTEGER NOT NULL DEFAULT 0,
 message TEXT NOT NULL
);
CREATE INDEX operation_requests_collector_time ON operation_requests(collector_id,started_at);
