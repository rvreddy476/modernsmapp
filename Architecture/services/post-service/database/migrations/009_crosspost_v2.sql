-- 009_crosspost_v2.sql: Profile Sync + Cross-Post spec v3 migrations
-- Adds: embed_ref on posts, crosspost_settings on video_metadata,
--        crosspost_links table, user_preferences.defaults column

-- ─── M3: Add embed_ref to posts for cross-post embed rendering ────────
ALTER TABLE posts ADD COLUMN IF NOT EXISTS embed_ref JSONB;

COMMENT ON COLUMN posts.embed_ref IS 'Structured embed reference for video_embed/flick_embed cross-posts';

-- ─── M4: Add content_type values for embeds ───────────────────────────
-- content_type already supports: post, poll, reel, video, flick, long_video
-- We need: video_embed, flick_embed
-- The existing CHECK constraint from migration 003 needs updating.
-- Drop old constraint and recreate with new values.
DO $$
BEGIN
    -- Drop existing constraint if it exists (name may vary)
    ALTER TABLE posts DROP CONSTRAINT IF EXISTS posts_content_type_check;
    ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_content_type;
EXCEPTION WHEN OTHERS THEN
    NULL;
END $$;

ALTER TABLE posts ADD CONSTRAINT chk_posts_content_type
    CHECK (content_type IN ('post', 'poll', 'reel', 'video', 'flick', 'long_video', 'video_embed', 'flick_embed'));

-- ─── M5: Add crosspost_settings to video_metadata ────────────────────
ALTER TABLE video_metadata ADD COLUMN IF NOT EXISTS crosspost_settings JSONB NOT NULL DEFAULT '{}';

COMMENT ON COLUMN video_metadata.crosspost_settings IS 'Per-upload cross-post preferences (e.g., auto_crosspost_to_postbook)';

-- ─── M6: Create crosspost_links (replaces reel_crosspost) ────────────
CREATE TABLE IF NOT EXISTS crosspost_links (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_module   TEXT NOT NULL CHECK (source_module IN ('posttube', 'postgram')),
    source_post_id  UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    target_module   TEXT NOT NULL CHECK (target_module IN ('postbook')),
    target_post_id  UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

-- Partial unique index: only one active cross-post per source+target_module
CREATE UNIQUE INDEX IF NOT EXISTS idx_crosspost_links_unique_active
    ON crosspost_links(source_module, source_post_id, target_module)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_crosspost_links_source
    ON crosspost_links(source_post_id)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_crosspost_links_target
    ON crosspost_links(target_post_id)
    WHERE deleted_at IS NULL;

COMMENT ON TABLE crosspost_links IS 'Module-based cross-post links (replaces reel_crosspost). Partial unique index supports re-crosspost after removal.';

-- ─── M7: Migrate data from reel_crosspost → crosspost_links ──────────
-- Only migrate rows that have status='published' and target_type='feed' (Postbook)
INSERT INTO crosspost_links (id, source_module, source_post_id, target_module, target_post_id, created_at)
SELECT
    rc.id,
    'posttube',
    rc.source_reel_id,
    'postbook',
    rc.source_reel_id,  -- In old schema, target was same post with publish_to_feed=true
    rc.created_at
FROM reel_crosspost rc
WHERE rc.status = 'published'
  AND rc.target_type = 'feed'
  AND NOT EXISTS (SELECT 1 FROM crosspost_links cl WHERE cl.id = rc.id)
ON CONFLICT DO NOTHING;

-- Rename old table instead of dropping (safety)
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'reel_crosspost' AND table_schema = 'public') THEN
        ALTER TABLE reel_crosspost RENAME TO reel_crosspost_deprecated;
    END IF;
EXCEPTION WHEN OTHERS THEN
    NULL;
END $$;

-- ─── M8: Add defaults column to user_preferences ─────────────────────
ALTER TABLE user_preferences ADD COLUMN IF NOT EXISTS defaults JSONB NOT NULL DEFAULT '{}';

COMMENT ON COLUMN user_preferences.defaults IS 'Per-module default settings (e.g., auto_crosspost_to_postbook)';

-- ─── Index for My Uploads queries ─────────────────────────────────────
-- Videos: author's long videos ordered by created_at
CREATE INDEX IF NOT EXISTS idx_posts_author_video_uploads
    ON posts(author_id, created_at DESC)
    WHERE content_type IN ('video', 'long_video') AND deleted_at IS NULL;

-- Flicks/reels: author's short-form video uploads
CREATE INDEX IF NOT EXISTS idx_posts_author_flick_uploads
    ON posts(author_id, created_at DESC)
    WHERE content_type IN ('flick', 'reel') AND deleted_at IS NULL;

-- Text/image posts only (for My Uploads Posts tab)
CREATE INDEX IF NOT EXISTS idx_posts_author_text_image
    ON posts(author_id, created_at DESC)
    WHERE content_type IN ('post', 'image') AND deleted_at IS NULL;

-- Embed posts (for cascade delete lookups)
CREATE INDEX IF NOT EXISTS idx_posts_embed_type
    ON posts(author_id, created_at DESC)
    WHERE content_type IN ('video_embed', 'flick_embed') AND deleted_at IS NULL;
