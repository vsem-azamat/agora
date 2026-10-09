-- pull requests the hub follows for each agent: found from its branch, or declared (agents.prs)
CREATE TABLE pull_requests (
    agent    TEXT    NOT NULL REFERENCES agents (name),
    repo     TEXT    NOT NULL,           -- forge host and path: github.com/example-org/example-app
    number   INTEGER NOT NULL,
    found    INTEGER NOT NULL DEFAULT 0, -- 1: found from the agent's branch; 0: followed because declared
    reported TEXT    NOT NULL DEFAULT '', -- the CI state last reported and its head commit: "green <sha>"
    PRIMARY KEY (agent, repo, number)
);
