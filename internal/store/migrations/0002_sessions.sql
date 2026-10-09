CREATE TABLE agents (
    name      TEXT PRIMARY KEY,
    joined_at INTEGER NOT NULL -- unix milliseconds
);

CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,
    kind        TEXT    NOT NULL,
    agent       TEXT REFERENCES agents (name),
    pid         INTEGER NOT NULL DEFAULT 0,
    pid_start   INTEGER NOT NULL DEFAULT 0, -- the process start time, to tell a reused pid apart
    cwd         TEXT    NOT NULL DEFAULT '',
    terminal    TEXT    NOT NULL DEFAULT '',
    state       TEXT    NOT NULL CHECK (state IN ('busy', 'idle', 'ended')),
    state_at    INTEGER NOT NULL,
    started_at  INTEGER NOT NULL,
    seen_at     INTEGER NOT NULL,            -- last event from the session
    noted       TEXT    NOT NULL DEFAULT '', -- the queue note last added to the agent's context
    checked_at  INTEGER NOT NULL DEFAULT 0   -- last tool-use check, for throttling
);

-- A name belongs to at most one session that has not ended.
CREATE UNIQUE INDEX sessions_live_agent ON sessions (agent) WHERE agent IS NOT NULL AND state != 'ended';
