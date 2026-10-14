-- a subscription made by following the agent's project room (join or set with a project), which
-- stops when the project changes unless the agent touched it since; any explicit follow, mode
-- change or unfollow by the agent clears the mark
ALTER TABLE subscriptions ADD COLUMN by_project INTEGER NOT NULL DEFAULT 0 CHECK (by_project IN (0, 1));
