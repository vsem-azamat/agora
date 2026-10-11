-- 1 for a note: a message of a bridged room that stays in Agora; it is never handed to the
-- bridge, so it never has a delivery state
ALTER TABLE messages ADD COLUMN note INTEGER NOT NULL DEFAULT 0 CHECK (note IN (0, 1) AND (note = 0 OR delivery IS NULL));
