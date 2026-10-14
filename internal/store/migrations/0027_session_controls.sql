-- Compactions and interrupts that agents ask for. A compact waits queued until its session is
-- idle; an interrupt is recorded when it has run.
CREATE TABLE session_controls (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id   TEXT    NOT NULL REFERENCES sessions (id),
    agent        TEXT    NOT NULL, -- the agent bound to the session when it was asked for
    action       TEXT    NOT NULL CHECK (action IN ('compact', 'interrupt')),
    focus        TEXT    NOT NULL DEFAULT '',
    requested_by TEXT    NOT NULL,
    requested_at INTEGER NOT NULL,
    state        TEXT    NOT NULL CHECK (state IN ('queued', 'running', 'done', 'failed', 'dropped')),
    ran_at       INTEGER NOT NULL DEFAULT 0, -- when the command started
    result       TEXT    NOT NULL DEFAULT '',
    told         INTEGER NOT NULL DEFAULT 0  -- the target's context has carried the note
);

-- A session has at most one compact that waits or runs.
CREATE UNIQUE INDEX session_controls_open_compact ON session_controls (session_id)
    WHERE action = 'compact' AND state IN ('queued', 'running');
CREATE INDEX session_controls_untold ON session_controls (session_id) WHERE state = 'done' AND told = 0;
