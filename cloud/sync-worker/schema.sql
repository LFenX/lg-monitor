-- Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor
-- Cloudflare D1 schema for lgmon multi-device cloud sync (sync.lfen.ink).
-- Apply locally:  wrangler d1 execute lgmon-sync --local  --file=./schema.sql
-- Apply remote:   wrangler d1 execute lgmon-sync --remote --file=./schema.sql

CREATE TABLE IF NOT EXISTS users (
  id             TEXT PRIMARY KEY,
  email          TEXT NOT NULL UNIQUE,
  password_hash  TEXT,
  password_salt  TEXT,
  -- Monotonic per-user change counter; clients pull with since=<last seq seen>.
  sync_seq       INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS otp_codes (
  id          TEXT PRIMARY KEY,
  email       TEXT NOT NULL,
  purpose     TEXT NOT NULL DEFAULT 'login', -- 'register' | 'login' | 'reset'
  code_hash   TEXT NOT NULL,
  salt        TEXT NOT NULL,
  attempts    INTEGER NOT NULL DEFAULT 0,
  expires_at  INTEGER NOT NULL,
  used_at     INTEGER,
  created_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_otp_email_created ON otp_codes (email, created_at DESC);

CREATE TABLE IF NOT EXISTS sessions (
  token_hash  TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id   TEXT NOT NULL,
  created_at  INTEGER NOT NULL,
  last_seen   INTEGER NOT NULL,
  expires_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions (user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_device ON sessions (user_id, device_id);

-- One row per bound device (UPSERT only; no time-series history accumulates in D1).
CREATE TABLE IF NOT EXISTS devices (
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_id   TEXT NOT NULL,
  name        TEXT NOT NULL,
  avatar      TEXT NOT NULL DEFAULT '',
  os          TEXT NOT NULL DEFAULT '',
  version     TEXT NOT NULL DEFAULT '',
  meta_hash   TEXT NOT NULL DEFAULT '',
  meta_json   TEXT NOT NULL DEFAULT '',
  meta_seq    INTEGER NOT NULL DEFAULT 0,
  state_hash  TEXT NOT NULL DEFAULT '',
  state_json  TEXT NOT NULL DEFAULT '',
  state_seq   INTEGER NOT NULL DEFAULT 0,
  seq         INTEGER NOT NULL DEFAULT 0,
  last_seen   INTEGER NOT NULL,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  PRIMARY KEY (user_id, device_id)
);

CREATE INDEX IF NOT EXISTS idx_devices_user_seq ON devices (user_id, seq);

-- Incremental service Markdown docs, uploaded only when content hash changes and fetched on demand.
CREATE TABLE IF NOT EXISTS device_docs (
  user_id     TEXT NOT NULL,
  device_id   TEXT NOT NULL,
  monitor_id  TEXT NOT NULL,
  doc_hash    TEXT NOT NULL,
  markdown    TEXT NOT NULL,
  updated_at  INTEGER NOT NULL,
  PRIMARY KEY (user_id, device_id, monitor_id),
  FOREIGN KEY (user_id, device_id) REFERENCES devices(user_id, device_id) ON DELETE CASCADE
);

-- Incremental batch job daily records (30-day JobDays), uploaded only when job hash changes and fetched on demand.
CREATE TABLE IF NOT EXISTS device_jobs (
  user_id      TEXT NOT NULL,
  device_id    TEXT NOT NULL,
  monitor_id   TEXT NOT NULL,
  job_hash     TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  updated_at   INTEGER NOT NULL,
  PRIMARY KEY (user_id, device_id, monitor_id),
  FOREIGN KEY (user_id, device_id) REFERENCES devices(user_id, device_id) ON DELETE CASCADE
);

-- Lightweight rate-limit buckets for OTP sends and password attempts.
CREATE TABLE IF NOT EXISTS rate_limits (
  bucket      TEXT PRIMARY KEY,
  count       INTEGER NOT NULL,
  window_end  INTEGER NOT NULL
);
