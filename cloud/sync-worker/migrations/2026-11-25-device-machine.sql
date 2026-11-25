-- Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor
-- Run once on a database created before 2026-11-25, before deploying the worker that uses these columns:
--   npx wrangler d1 execute lgmon-sync --remote -c wrangler.local.toml --file=./migrations/2026-11-25-device-machine.sql
ALTER TABLE devices ADD COLUMN machine TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN remember INTEGER NOT NULL DEFAULT 1;
