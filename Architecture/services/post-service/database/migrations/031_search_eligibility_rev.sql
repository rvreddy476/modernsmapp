-- Module 2 M2-P0-2 — monotonic search-eligibility revision.
--
-- Search must be able to reject a stale eligibility event. Without a
-- per-post monotonic revision, a late-delivered "approved" can arrive
-- after a "rejected"/"taken down" and RESURRECT unsafe content in the
-- public index. Kafka only orders within a partition, and retries/DLQ
-- replays reorder by construction, so ordering cannot be assumed.
--
-- Every path that changes review_status or visibility bumps this in the
-- same UPDATE, and publishes the new value on the transition event. The
-- search consumer applies an event only when its revision is strictly
-- greater than the last revision applied for that post.
--
-- Creation sets 1 (see post_search_rev_default), so a PostCreated event
-- and the first transition are already ordered relative to each other.
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS search_rev BIGINT NOT NULL DEFAULT 1;

-- Index supports the reconciler's scan for contaminated projections.
CREATE INDEX IF NOT EXISTS idx_posts_search_eligibility
    ON posts (visibility, review_status)
    WHERE deleted_at IS NULL;
