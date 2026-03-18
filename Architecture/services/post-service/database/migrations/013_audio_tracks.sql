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
