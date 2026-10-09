CREATE TABLE resources (
    key       TEXT PRIMARY KEY,
    slots     INTEGER NOT NULL DEFAULT 1 CHECK (slots >= 1),
    slots_set INTEGER NOT NULL DEFAULT 0 -- 1 when the slots were set explicitly
);

CREATE TABLE entries (
    key        TEXT    NOT NULL REFERENCES resources (key),
    agent      TEXT    NOT NULL,
    note       TEXT    NOT NULL DEFAULT '',
    state      TEXT    NOT NULL CHECK (state IN ('waiting', 'offered', 'held')),
    seq        INTEGER NOT NULL, -- queue order within the resource
    lease_ms   INTEGER NOT NULL,
    joined_at  INTEGER NOT NULL, -- unix milliseconds
    expires_at INTEGER,          -- lease end when held, claim deadline when offered
    skips      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (key, agent)
);

CREATE INDEX entries_order ON entries (key, seq);
CREATE INDEX entries_expiry ON entries (expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE removals (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    at    INTEGER NOT NULL,
    key   TEXT    NOT NULL,
    agent TEXT    NOT NULL, -- removed agent
    actor TEXT    NOT NULL  -- who forced the removal
);
