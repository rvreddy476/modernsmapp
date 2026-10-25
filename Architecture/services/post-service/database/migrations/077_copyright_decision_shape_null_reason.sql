-- 077: the copyright decision-shape CHECK refuses a reviewer-less block with
-- no reason (follow-up to 075; 075 is applied and is not edited).
--
-- 075 wrote the blocked branch as
--     decided_by IS NOT NULL OR reason = 'owner_registered'
-- For a block with decided_by NULL and reason NULL that is FALSE OR NULL =
-- NULL, so the branch, and with it the whole CHECK, is NULL, and a CHECK
-- passes on NULL. The one shape the constraint exists to refuse (a block
-- nobody decided and the system did not claim) was accepted.
--
-- IS NOT DISTINCT FROM is two-valued: NULL compares as FALSE, so a NULL
-- reason is "not owner_registered" and the block needs a named reviewer.
-- Every other branch is unchanged. Existing rows are validated: the
-- service always writes reason 'owner_registered' with a system block
-- (copyright_owner_match.go), so a row this refuses is the bad data the
-- constraint was meant to keep out.

ALTER TABLE copyright_title_reviews DROP CONSTRAINT IF EXISTS copyright_title_reviews_decision_shape;
ALTER TABLE copyright_title_reviews ADD CONSTRAINT copyright_title_reviews_decision_shape CHECK (
    (state = 'pending' AND decided_at IS NULL AND decided_by IS NULL)
    OR (state = 'blocked' AND decided_at IS NOT NULL
        AND (decided_by IS NOT NULL OR reason IS NOT DISTINCT FROM 'owner_registered'))
    OR (state = 'cleared' AND decided_at IS NOT NULL));
