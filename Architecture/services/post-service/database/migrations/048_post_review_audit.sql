-- Admin plan lane A4: an append-only audit of the two internal post-state
-- endpoints, POST /v1/posts/internal/review-status and
-- POST /v1/posts/internal/visibility.
--
-- post_moderation_decisions does not fit: it needs a UUID actor, a required
-- reason and an approve/reject/needs_changes action, and it has no notion of
-- visibility. The reviewer-service ML pre-filter and promotion worker call these
-- endpoints with the internal service key and no user, so the actor here is
-- either a verified gateway user (actor_type 'user', actor_user_id set) or a
-- service credential (actor_type 'service', actor_service set).
--
-- No FK to posts: the audit must outlive a post purge. Rows carry no content.
CREATE TABLE IF NOT EXISTS post_review_audit (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id         UUID NOT NULL,
    field           TEXT NOT NULL CHECK (field IN ('review_status','visibility')),
    previous_value  TEXT NOT NULL,
    new_value       TEXT NOT NULL,
    actor_type      TEXT NOT NULL CHECK (actor_type IN ('user','service')),
    actor_user_id   UUID,
    actor_service   TEXT,
    reason          TEXT CHECK (reason IS NULL OR length(reason) <= 2000),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (
        (actor_type = 'user'    AND actor_user_id IS NOT NULL AND actor_service IS NULL) OR
        (actor_type = 'service' AND actor_user_id IS NULL AND actor_service IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_post_review_audit_post
    ON post_review_audit (post_id, created_at DESC);

-- Append-only: refuse UPDATE and DELETE at the database.
CREATE OR REPLACE FUNCTION post_review_audit_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'post_review_audit is append-only';
END;
$$;

DROP TRIGGER IF EXISTS trg_post_review_audit_append_only ON post_review_audit;
CREATE TRIGGER trg_post_review_audit_append_only
    BEFORE UPDATE OR DELETE ON post_review_audit
    FOR EACH ROW EXECUTE FUNCTION post_review_audit_append_only();
