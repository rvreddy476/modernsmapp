-- Module 1 P0-5: server-side drafts + scheduling for all composer post
-- types (text, photo, carousel, poll, article). Reel/long-video drafts
-- stay in reel_drafts (legacy endpoints preserved); both feed the same
-- in-process publish worker.
--
-- Multi-replica safety: due drafts are claimed with an atomic
-- status transition + FOR UPDATE SKIP LOCKED. The draft id doubles as the
-- published post id (INSERT posts PK = draft id), so a crash between
-- post-insert and status flip re-claims and hits the PK conflict instead
-- of double-publishing: exactly one post, exactly one PostCreated event.
CREATE TABLE IF NOT EXISTS post_drafts (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id         UUID NOT NULL,
    post_type         TEXT NOT NULL DEFAULT 'post'
                      CHECK (post_type IN ('post','poll','article')),
    payload           JSONB NOT NULL DEFAULT '{}'::jsonb,
    schedule_at       TIMESTAMPTZ,
    status            TEXT NOT NULL DEFAULT 'draft'
                      CHECK (status IN ('draft','publishing','published','blocked','deleted')),
    blocked_reason    TEXT,
    published_post_id UUID,
    claimed_at        TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_post_drafts_author
    ON post_drafts (author_id, updated_at DESC)
    WHERE status IN ('draft','blocked');
CREATE INDEX IF NOT EXISTS idx_post_drafts_due
    ON post_drafts (schedule_at)
    WHERE status = 'draft' AND schedule_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_post_drafts_stale_claims
    ON post_drafts (claimed_at)
    WHERE status = 'publishing';
