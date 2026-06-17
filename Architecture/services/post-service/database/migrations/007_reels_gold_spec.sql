-- 007_reels_gold_spec.sql: Gold Spec production tables for Reels system
-- Covers: reel_hashtags, reel_crosspost, slug_history, moderation_reviews,
--         post_outbox_events, idempotency_keys

-- ─── Reel Hashtags ──────────────────────────────────────────────────
-- Normalized hashtag storage for efficient hashtag-based queries.
CREATE TABLE IF NOT EXISTS reel_hashtags (
    reel_id     UUID NOT NULL,
    hashtag     TEXT NOT NULL,
    position    INT NOT NULL DEFAULT 0,  -- position in caption for highlighting
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (reel_id, hashtag)
);

CREATE INDEX IF NOT EXISTS idx_reel_hashtags_tag ON reel_hashtags(hashtag);
CREATE INDEX IF NOT EXISTS idx_reel_hashtags_tag_recent ON reel_hashtags(hashtag, created_at DESC);

COMMENT ON TABLE reel_hashtags IS 'Normalized hashtag extraction from reel captions for search and trending';

-- ─── Reel Cross-Post ────────────────────────────────────────────────
-- Tracks cross-post intents: reel published to multiple destinations (feed, stories, groups).
CREATE TABLE IF NOT EXISTS reel_crosspost (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_reel_id  UUID NOT NULL,
    target_type     TEXT NOT NULL,           -- 'feed', 'story', 'group', 'page'
    target_id       TEXT,                    -- group_id/page_id if applicable
    status          TEXT NOT NULL DEFAULT 'pending',  -- pending, published, failed
    idempotency_key TEXT,
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ,
    UNIQUE (source_reel_id, target_type, target_id)
);

CREATE INDEX IF NOT EXISTS idx_reel_crosspost_source ON reel_crosspost(source_reel_id);
CREATE INDEX IF NOT EXISTS idx_reel_crosspost_status ON reel_crosspost(status) WHERE status = 'pending';

COMMENT ON TABLE reel_crosspost IS 'Cross-post intent storage with idempotent reference creation';

-- ─── Slug History ───────────────────────────────────────────────────
-- Tracks slug changes for SEO redirects (old slug → current reel).
CREATE TABLE IF NOT EXISTS slug_history (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reel_id     UUID NOT NULL,
    old_slug    TEXT NOT NULL,
    new_slug    TEXT NOT NULL,
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_slug_history_old ON slug_history(old_slug);
CREATE INDEX IF NOT EXISTS idx_slug_history_reel ON slug_history(reel_id);

COMMENT ON TABLE slug_history IS 'SEO slug redirect history — old slugs 301-redirect to current';

-- ─── Moderation Reviews ─────────────────────────────────────────────
-- Tracks moderation decisions for reels (auto-scan + human review).
CREATE TABLE IF NOT EXISTS moderation_reviews (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reel_id         UUID NOT NULL,
    reviewer_type   TEXT NOT NULL,          -- 'auto', 'human'
    reviewer_id     TEXT,                   -- NULL for auto, moderator user_id for human
    decision        TEXT NOT NULL,          -- 'approved', 'rejected', 'flagged', 'pending_review'
    reason          TEXT,
    confidence      FLOAT,                 -- auto-scan confidence score (0.0-1.0)
    policy_violated TEXT,                   -- which policy was violated
    metadata        JSONB,                 -- additional scan results
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_moderation_reviews_reel ON moderation_reviews(reel_id);
CREATE INDEX IF NOT EXISTS idx_moderation_reviews_decision ON moderation_reviews(decision) WHERE decision IN ('flagged', 'pending_review');

COMMENT ON TABLE moderation_reviews IS 'Auto-scan and human moderation review audit trail';

-- ─── Outbox Events ──────────────────────────────────────────────────
-- Transactional outbox pattern for reliable Kafka event publishing.
CREATE TABLE IF NOT EXISTS post_outbox_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type      TEXT NOT NULL,
    aggregate_type  TEXT NOT NULL,          -- 'reel', 'post', 'draft'
    aggregate_id    UUID NOT NULL,
    payload         JSONB NOT NULL,
    published       BOOLEAN NOT NULL DEFAULT FALSE,
    published_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON post_outbox_events(created_at ASC) WHERE published = FALSE;
CREATE INDEX IF NOT EXISTS idx_outbox_aggregate ON post_outbox_events(aggregate_type, aggregate_id);

COMMENT ON TABLE post_outbox_events IS 'Transactional outbox for reliable event publishing to Kafka';

-- ─── Idempotency Keys ──────────────────────────────────────────────
-- Prevents duplicate operations (e.g., double-publish, double-crosspost).
CREATE TABLE IF NOT EXISTS idempotency_keys (
    key             TEXT PRIMARY KEY,
    result_status   INT NOT NULL,           -- HTTP status code of the original response
    result_body     JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours')
);

CREATE INDEX IF NOT EXISTS idx_idempotency_expiry ON idempotency_keys(expires_at);

COMMENT ON TABLE idempotency_keys IS 'Request deduplication — store result of first request, replay on retry';

-- ─── Additional columns on posts table for Gold Spec ────────────────
-- original_reel_id for remix chain, is_made_for_kids, is_branded, is_paid_promotion,
-- deleted_at for soft delete, topic_id for structured topics
ALTER TABLE posts ADD COLUMN IF NOT EXISTS original_reel_id UUID;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS is_branded BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS topic_id UUID;

-- Add similar columns to reel_drafts
ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS original_reel_id UUID;
ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS is_branded BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS topic_id UUID;

-- Soft-delete partial index for efficient querying of non-deleted posts
CREATE INDEX IF NOT EXISTS idx_posts_not_deleted ON posts(created_at DESC) WHERE deleted_at IS NULL;

-- ─── Topics Lookup Table ────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS topics (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    slug        TEXT NOT NULL UNIQUE,
    parent_id   UUID REFERENCES topics(id),
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO topics (name, slug) VALUES
    ('Entertainment', 'entertainment'),
    ('Music', 'music'),
    ('Sports', 'sports'),
    ('Gaming', 'gaming'),
    ('News & Politics', 'news-politics'),
    ('Education', 'education'),
    ('Science & Technology', 'science-technology'),
    ('Comedy', 'comedy'),
    ('Fashion & Beauty', 'fashion-beauty'),
    ('Food & Cooking', 'food-cooking'),
    ('Travel', 'travel'),
    ('Fitness & Health', 'fitness-health'),
    ('Art & Creativity', 'art-creativity'),
    ('Pets & Animals', 'pets-animals'),
    ('Business & Finance', 'business-finance')
ON CONFLICT (name) DO NOTHING;
