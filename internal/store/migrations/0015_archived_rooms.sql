-- an archived room keeps its messages, subscriptions and reading positions, but takes no new
-- messages and counts as unread for no one; archived_by is the agent that archived it
ALTER TABLE rooms ADD COLUMN archived_at INTEGER;
ALTER TABLE rooms ADD COLUMN archived_by TEXT CHECK ((archived_at IS NULL) = (archived_by IS NULL));
