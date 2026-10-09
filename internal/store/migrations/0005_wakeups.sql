ALTER TABLE sessions ADD COLUMN turn INTEGER NOT NULL DEFAULT 0;         -- counts prompts and starts
ALTER TABLE sessions ADD COLUMN woken_for TEXT NOT NULL DEFAULT '';    -- what the last command wake was for
ALTER TABLE sessions ADD COLUMN woken_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN wake_result TEXT NOT NULL DEFAULT '';  -- outcome of the last command wake
