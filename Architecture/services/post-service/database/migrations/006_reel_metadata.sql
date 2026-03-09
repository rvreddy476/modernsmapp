-- 006_reel_metadata.sql
-- Adds reel-specific metadata columns to posts and creates the reel_drafts table
-- for the full draft → publish workflow with YouTube-style fields.

-- ────────────────────────────────────────────────────────────
-- 1. Extend posts table with reel metadata columns
-- ────────────────────────────────────────────────────────────

ALTER TABLE posts ADD COLUMN IF NOT EXISTS title TEXT DEFAULT '';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS tags TEXT[] DEFAULT '{}';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS category TEXT DEFAULT '';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS language TEXT DEFAULT 'en';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS seo_title TEXT DEFAULT '';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS paid_promotion BOOLEAN DEFAULT FALSE;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS altered_content BOOLEAN DEFAULT FALSE;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS is_made_for_kids BOOLEAN DEFAULT FALSE;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS license TEXT DEFAULT 'standard';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS allow_embedding BOOLEAN DEFAULT TRUE;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS publish_to_feed BOOLEAN DEFAULT TRUE;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS remix_setting TEXT DEFAULT 'allow';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS comment_moderation TEXT DEFAULT 'none';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS comment_access TEXT DEFAULT 'everyone';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS recording_date DATE;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS recording_location TEXT DEFAULT '';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS cover_media_id UUID;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS original_audio_volume REAL DEFAULT 1.0;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS overlay_audio_volume REAL DEFAULT 1.0;

-- Update visibility constraint to include 'unlisted'
ALTER TABLE posts DROP CONSTRAINT IF EXISTS posts_visibility_check;
ALTER TABLE posts ADD CONSTRAINT posts_visibility_check
    CHECK (visibility IN ('public', 'followers', 'private', 'unlisted'));

-- ────────────────────────────────────────────────────────────
-- 2. reel_drafts table — full draft workflow
-- ────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS reel_drafts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id       UUID NOT NULL,
    media_id        UUID,

    -- Content
    title           TEXT NOT NULL DEFAULT '',
    caption         TEXT NOT NULL DEFAULT '',
    hashtags        TEXT[] DEFAULT '{}',
    tags            TEXT[] DEFAULT '{}',

    -- Distribution
    visibility      TEXT NOT NULL DEFAULT 'public'
                    CHECK (visibility IN ('public', 'followers', 'private', 'unlisted')),
    topic_id        INT,
    category        TEXT DEFAULT '',
    language        TEXT DEFAULT 'en',
    seo_title       TEXT DEFAULT '',

    -- Cross-post
    cross_post_postbook BOOLEAN DEFAULT TRUE,
    cross_post_posttube BOOLEAN DEFAULT FALSE,
    publish_to_feed     BOOLEAN DEFAULT TRUE,

    -- Compliance / Disclosure
    is_made_for_kids    BOOLEAN DEFAULT FALSE,
    paid_promotion      BOOLEAN DEFAULT FALSE,
    altered_content     BOOLEAN DEFAULT FALSE,

    -- Smart features
    auto_chapters       BOOLEAN DEFAULT TRUE,
    featured_places     BOOLEAN DEFAULT TRUE,
    auto_concepts       BOOLEAN DEFAULT TRUE,

    -- Rights / Permissions
    license             TEXT DEFAULT 'standard' CHECK (license IN ('standard', 'creative_commons')),
    allow_embedding     BOOLEAN DEFAULT TRUE,
    remix_setting       TEXT DEFAULT 'allow' CHECK (remix_setting IN ('allow', 'allow_audio_only', 'disallow')),

    -- Comments & Ratings
    likes_enabled       BOOLEAN DEFAULT TRUE,
    comments_enabled    BOOLEAN DEFAULT TRUE,
    comment_moderation  TEXT DEFAULT 'basic' CHECK (comment_moderation IN ('none', 'basic', 'strict', 'hold_all')),
    comment_access      TEXT DEFAULT 'everyone' CHECK (comment_access IN ('everyone', 'followers', 'nobody')),

    -- Recording metadata
    recording_date      DATE,
    recording_location  TEXT DEFAULT '',

    -- Audio
    audio_track_id      TEXT,
    audio_start_ms      INT DEFAULT 0,
    original_audio_volume REAL DEFAULT 1.0,
    overlay_audio_volume  REAL DEFAULT 1.0,

    -- Cover
    cover_media_id      UUID,

    -- Scheduling & Status
    schedule_at         TIMESTAMPTZ,
    status              TEXT NOT NULL DEFAULT 'draft'
                        CHECK (status IN ('draft', 'processing', 'publishing_pending', 'published', 'rejected', 'deleted')),
    moderation_status   TEXT DEFAULT 'pending'
                        CHECK (moderation_status IN ('pending', 'approved', 'flagged', 'rejected')),
    published_post_id   UUID,  -- links to posts.id after publish

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_reel_drafts_author ON reel_drafts (author_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_reel_drafts_schedule ON reel_drafts (schedule_at)
    WHERE schedule_at IS NOT NULL AND status = 'draft';
