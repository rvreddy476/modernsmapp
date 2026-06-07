-- In-video product tags. Bridges post-service posts → monetization-service
-- affiliate_links so a creator can tag products inside a video and viewers
-- get a tappable overlay card → /v1/commerce/listings/<id>?via=<code>.
--
-- The affiliate_link_id stays an opaque UUID here (no FK) because it lives
-- in a different logical database (monetization vs. app). Cross-service
-- validation happens at the service layer when the tag is created.

CREATE TABLE IF NOT EXISTS post_product_tags (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id           UUID NOT NULL,
    -- Cross-service reference into monetization.affiliate_links.id.
    affiliate_link_id UUID NOT NULL,
    -- The creator who owns this tag. Denormalised so the per-tag delete
    -- permission check doesn't have to join back to posts.
    creator_id        UUID NOT NULL,

    -- Display window inside the video. NULL start AND NULL end means
    -- "show across the whole video" — for image posts the time fields
    -- are ignored.
    time_start_ms     INT,
    time_end_ms       INT,

    -- Overlay anchor as percentages of the player viewport (0..100).
    -- 50/50 = dead centre. The mobile/web player decides exact pixel
    -- offset + collision avoidance; the server just stores intent.
    position_x        REAL,
    position_y        REAL,

    -- Display payload. Cached at tag-creation time so the player
    -- doesn't have to join through to commerce on every view. Refreshed
    -- when the underlying listing changes (best-effort, via the
    -- product-update event consumer — follow-up).
    label             TEXT NOT NULL DEFAULT '',
    image_url         TEXT NOT NULL DEFAULT '',

    -- Counters. Player increments via the impression/click endpoints;
    -- these are eventually-consistent (Redis flush worker, same shape as
    -- the engagement counter story).
    impression_count  BIGINT NOT NULL DEFAULT 0,
    click_count       BIGINT NOT NULL DEFAULT 0,

    is_active         BOOLEAN NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- CHECK: position percentages, when set, must be 0..100.
    CONSTRAINT post_product_tags_pos_x_check
        CHECK (position_x IS NULL OR (position_x >= 0 AND position_x <= 100)),
    CONSTRAINT post_product_tags_pos_y_check
        CHECK (position_y IS NULL OR (position_y >= 0 AND position_y <= 100)),
    -- CHECK: time window, when both set, must be ordered.
    CONSTRAINT post_product_tags_time_window_check
        CHECK (time_start_ms IS NULL OR time_end_ms IS NULL OR time_end_ms >= time_start_ms)
);

-- One tag for the same (post, affiliate_link) pair — prevents accidental
-- double-tagging of the same product in the same video.
CREATE UNIQUE INDEX IF NOT EXISTS idx_post_product_tags_unique
    ON post_product_tags(post_id, affiliate_link_id)
    WHERE is_active = TRUE;

-- Read path: list active tags for a post (called on every player open).
CREATE INDEX IF NOT EXISTS idx_post_product_tags_post
    ON post_product_tags(post_id, is_active)
    WHERE is_active = TRUE;

-- Creator-side: list every tag the creator has placed (for the
-- creator-analytics dashboard).
CREATE INDEX IF NOT EXISTS idx_post_product_tags_creator
    ON post_product_tags(creator_id, created_at DESC)
    WHERE is_active = TRUE;
