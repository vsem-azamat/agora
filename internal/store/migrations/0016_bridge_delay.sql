-- the outbound policies are open, delay and read: approve became delay, which sends an agent's
-- message once the hub's delay has passed unless the operator cancels it. SQLite cannot change a
-- CHECK, so the bridges table is built again; dropping it deletes the rows of bridge_agents
-- through their foreign key, so they are kept aside and put back.
CREATE TEMP TABLE kept_bridge_agents AS SELECT bridge, agent FROM bridge_agents;

CREATE TABLE bridges_new (
    name       TEXT PRIMARY KEY REFERENCES rooms (name),
    command    TEXT    NOT NULL, -- run with sh -c
    policy     TEXT    NOT NULL DEFAULT 'delay' CHECK (policy IN ('open', 'delay', 'read')),
    created_by TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    cursor     TEXT    NOT NULL DEFAULT '' -- the latest cursor the bridge reported, passed in hello
);

INSERT INTO bridges_new (name, command, policy, created_by, created_at, cursor)
SELECT name, command, CASE policy WHEN 'approve' THEN 'delay' ELSE policy END, created_by, created_at, cursor
FROM bridges;

DROP TABLE bridges;
ALTER TABLE bridges_new RENAME TO bridges;

INSERT INTO bridge_agents (bridge, agent) SELECT bridge, agent FROM kept_bridge_agents;
DROP TABLE kept_bridge_agents;

-- when a message of a bridged room was scheduled to go out (delivery 'pending'); it goes out once
-- the hub's delay has passed since then. Messages that waited for the operator are scheduled now.
ALTER TABLE messages ADD COLUMN scheduled_at INTEGER;
UPDATE messages SET scheduled_at = CAST(strftime('%s', 'now') AS INTEGER) * 1000 WHERE delivery = 'pending';
