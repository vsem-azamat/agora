-- the sigil and pigment an agent chose; empty when unset
ALTER TABLE agents ADD COLUMN icon TEXT NOT NULL DEFAULT '';
ALTER TABLE agents ADD COLUMN pigment TEXT NOT NULL DEFAULT '';

-- names agents gave up by renaming themselves; a former name stays reserved for its agent
CREATE TABLE former_names (
    name       TEXT PRIMARY KEY,
    agent      TEXT    NOT NULL REFERENCES agents (name),
    renamed_at INTEGER NOT NULL -- unix milliseconds
);

CREATE INDEX former_names_agent ON former_names (agent);
