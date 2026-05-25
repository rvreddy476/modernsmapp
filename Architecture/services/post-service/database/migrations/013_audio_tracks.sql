-- Migration 013: Audio/Music Layer for posts
-- Tracks audio used in reels/videos, trending sounds, attribution, and reuse chain.

CREATE TABLE IF NOT EXISTS audio_tracks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    artist TEXT NOT NULL DEFAULT '',
    duration_ms INT NOT NULL DEFAULT 0,
    media_id UUID NOT NULL,
    original_post_id UUID,
    genre TEXT NOT NULL DEFAULT '',
    is_original BOOLEAN NOT NULL DEFAULT true,
    use_count INT NOT NULL DEFAULT 0,
    is_trending BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audio_trending ON audio_tracks(is_trending, use_count DESC) WHERE is_trending = true;
CREATE INDEX IF NOT EXISTS idx_audio_post ON audio_tracks(original_post_id);

ALTER TABLE posts ADD COLUMN IF NOT EXISTS audio_track_id UUID REFERENCES audio_tracks(id);

-- M10: audio-track ownership. is_public defaults TRUE so existing
-- tracks behave as before — anyone can use any track in their own
-- post (the TikTok/Reels reuse-by-default UX). When a seller-creator
-- uploads an original track they want gated, they set is_public=FALSE
-- and only the creator + grantees can attach it. creator_user_id is
-- backfilled from original_post_id.author when set.
ALTER TABLE audio_tracks
    ADD COLUMN IF NOT EXISTS is_public BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS creator_user_id UUID;
CREATE INDEX IF NOT EXISTS idx_audio_creator ON audio_tracks(creator_user_id) WHERE creator_user_id IS NOT NULL;
