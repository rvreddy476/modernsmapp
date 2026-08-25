-- Module 1 P0-1 + P0-4.
-- feed_distribution: normalized per-post main-feed state from
-- PostCreated / PostDistributionUpdated events; rev-guarded upserts make
-- replay and out-of-order delivery safe. No row = legacy post = eligible.
-- user_preferences.long_video_frequency: explicit viewer control for the
-- social home surface only (PostTube, subscriptions, direct links are
-- unaffected).
ALTER TABLE user_preferences
    ADD COLUMN IF NOT EXISTS long_video_frequency TEXT NOT NULL DEFAULT 'balanced'
        CHECK (long_video_frequency IN ('hidden','reduced','balanced','preferred'));

CREATE TABLE IF NOT EXISTS feed_distribution (
    post_id    UUID PRIMARY KEY,
    main_feed  BOOLEAN NOT NULL DEFAULT TRUE,
    rev        BIGINT  NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_feed_distribution_excluded
    ON feed_distribution (post_id) WHERE main_feed = FALSE;
