CREATE TABLE proposals (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    title      TEXT    NOT NULL,
    body       TEXT    NOT NULL,
    author     TEXT    NOT NULL REFERENCES agents (name),
    created_at INTEGER NOT NULL,
    state      TEXT    NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'accepted', 'rejected', 'withdrawn')),
    closed_by  TEXT,
    closed_at  INTEGER
);

CREATE TABLE votes (
    proposal_id INTEGER NOT NULL REFERENCES proposals (id),
    agent       TEXT    NOT NULL REFERENCES agents (name),
    choice      TEXT    NOT NULL CHECK (choice IN ('yes', 'no', 'abstain')),
    reason      TEXT    NOT NULL DEFAULT '',
    at          INTEGER NOT NULL,
    PRIMARY KEY (proposal_id, agent)
);

-- proposals that changed the charter: each accepted proposal changes it once
CREATE TABLE charter_changes (
    proposal_id INTEGER PRIMARY KEY REFERENCES proposals (id),
    changed_by  TEXT    NOT NULL,
    changed_at  INTEGER NOT NULL
);

-- at most one row: the current charter; without it the default charter applies
CREATE TABLE charter (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    body        TEXT    NOT NULL,
    changed_by  TEXT    NOT NULL,
    changed_at  INTEGER NOT NULL,
    proposal_id INTEGER REFERENCES proposals (id)
);
