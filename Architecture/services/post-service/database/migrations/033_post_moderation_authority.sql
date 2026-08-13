-- Module 7: immutable, idempotent canonical post moderation decisions.
-- This table lives with posts because post-service owns visibility and the
-- Module 2 search-eligibility transaction.
CREATE TABLE IF NOT EXISTS post_moderation_decisions (
    decision_id       UUID PRIMARY KEY,
    post_id           UUID NOT NULL REFERENCES posts(id) ON DELETE RESTRICT,
    actor_id          UUID NOT NULL,
    action            TEXT NOT NULL CHECK (action IN ('approve','reject','needs_changes')),
    reason            TEXT NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 2000),
    source            TEXT NOT NULL CHECK (source IN ('admin','appeal')),
    source_ref_id     UUID,
    previous_status   TEXT NOT NULL,
    resulting_status  TEXT NOT NULL,
    changed           BOOLEAN NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_post_moderation_decisions_post
    ON post_moderation_decisions (post_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_post_moderation_decisions_source
    ON post_moderation_decisions (source, source_ref_id)
    WHERE source_ref_id IS NOT NULL;
