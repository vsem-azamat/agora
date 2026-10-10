-- the CI state last reported for a followed pull request and the head commit it was reported
-- for get columns of their own, replacing the combined "green <sha>" value
ALTER TABLE pull_requests ADD COLUMN ci_state TEXT NOT NULL DEFAULT ''; -- green, red or conflict; empty until reported
ALTER TABLE pull_requests ADD COLUMN ci_head  TEXT NOT NULL DEFAULT ''; -- the head commit ci_state was reported for
UPDATE pull_requests SET ci_state = substr(reported, 1, instr(reported, ' ') - 1), ci_head = substr(reported, instr(reported, ' ') + 1)
    WHERE instr(reported, ' ') > 0;
ALTER TABLE pull_requests DROP COLUMN reported;
