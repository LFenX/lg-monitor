-- @column status_hour.confirmation_ms Minimum continuous failure duration used for this summary; zero means a legacy summary whose failure duration cannot be reconstructed without raw samples.
ALTER TABLE status_hour ADD COLUMN confirmation_ms INTEGER NOT NULL DEFAULT 0;
