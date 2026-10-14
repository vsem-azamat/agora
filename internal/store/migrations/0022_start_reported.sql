-- when the session last reported a start (not any other event), which a wait that runs at a
-- session start looks for
ALTER TABLE sessions ADD COLUMN start_reported_at INTEGER NOT NULL DEFAULT 0; -- unix milliseconds
