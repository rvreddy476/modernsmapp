-- Cross-posting: one composer action, several groups.
--
-- The copies are INDEPENDENT rows sharing a nullable id. Not a parent/child
-- foreign key: each copy lives under its own group's rules — one may be
-- published while another waits for approval and a third is deleted by its
-- moderators — and deleting the first must not cascade into groups that had
-- nothing to do with it.
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS cross_post_group_id UUID;

CREATE INDEX IF NOT EXISTS idx_group_posts_cross_post
    ON group_posts(cross_post_group_id) WHERE cross_post_group_id IS NOT NULL;

-- ONE ROW PER (request, target group) — not one per request.
--
-- This is the whole reason group_id is in the primary key. A retry after a
-- partial failure must COMPLETE the batch, not duplicate the part that
-- already worked: every target with a row here replays its original post id
-- and is not posted again, and only targets with no row are attempted.
--
-- outcome is stored so a replay reproduces the original ANSWER, including the
-- refusals. Re-evaluating the rules on a retry would let "2 of 5" silently
-- become "3 of 5" with the user having done nothing, because someone happened
-- to unban them in between.
CREATE TABLE IF NOT EXISTS group_post_requests (
    author_id       UUID NOT NULL,
    idempotency_key TEXT NOT NULL,
    group_id        UUID NOT NULL,
    -- NULL when the target was refused: there is no post to point at, but the
    -- row must still exist so the retry does not attempt it again.
    post_id         UUID,
    outcome         TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (author_id, idempotency_key, group_id)
);

CREATE INDEX IF NOT EXISTS idx_group_post_requests_lookup
    ON group_post_requests(author_id, idempotency_key);
