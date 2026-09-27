-- Migration 051: MTube "contracts first" (2026-09-27).
--
-- Six additive parts, every one guarded with IF NOT EXISTS / DO NOTHING so a
-- re-run is a no-op and a database that user-service booted first (its own
-- channels DDL already carries contact_email) is adapted rather than fought.
--
--   A. one video taxonomy: long_video rows the studio wrote free-text
--      categories into are folded onto the slugs GET /v1/posts/categories
--      now serves; anything unknown becomes 'other'.
--   B. live -> video: posts.source ('upload' | 'live') and posts.live_stream_id,
--      unique per stream so the VOD consumer is idempotent on redelivery.
--   C. channel branding: banner, links, contact email, featured video.
--   D. system collections: playlists.kind ('user' | 'watch_later' | 'liked'),
--      at most one of each system kind per owner.
--   E. creator comment tools: pinned_at (one per post) and hearted_at.
--   F. post_edit_audit: append-only record of every owner edit after publish
--      (PATCH /v1/posts/:id and the bulk visibility route), same shape and
--      same append-only trigger as post_review_audit (048).

-- ── A. Long-video categories onto the taxonomy ──────────────────────────────
--
-- Migration 047 already lowercased and hyphenated every stored value, so the
-- studio's YouTube-style names arrive here as e.g. 'film-&-animation',
-- 'science-&-technology', 'howto-&-style', 'news-&-politics'. The map below
-- names those spellings AND the bare words, so a value the studio sent
-- without the ampersand part ('film', 'science') lands on the same slug.
-- Slugs that already match a taxonomy id are left alone; everything else on
-- a long video becomes 'other' (empty stays empty: "no category" is a valid
-- state, not an unknown one).
UPDATE posts
   SET category = CASE category
       WHEN 'film-&-animation'      THEN 'film-animation'
       WHEN 'film-and-animation'    THEN 'film-animation'
       WHEN 'film-animation'        THEN 'film-animation'
       WHEN 'film'                  THEN 'film-animation'
       WHEN 'animation'             THEN 'film-animation'
       WHEN 'science-&-technology'  THEN 'science-tech'
       WHEN 'science-and-technology' THEN 'science-tech'
       WHEN 'science-technology'    THEN 'science-tech'
       WHEN 'science'               THEN 'science-tech'
       WHEN 'technology'            THEN 'science-tech'
       WHEN 'howto-&-style'         THEN 'howto-style'
       WHEN 'howto-and-style'       THEN 'howto-style'
       WHEN 'how-to-&-style'        THEN 'howto-style'
       WHEN 'howto'                 THEN 'howto-style'
       WHEN 'how-to'                THEN 'howto-style'
       WHEN 'style'                 THEN 'howto-style'
       WHEN 'people-&-blogs'        THEN 'people-blogs'
       WHEN 'people-and-blogs'      THEN 'people-blogs'
       WHEN 'people'                THEN 'people-blogs'
       WHEN 'blogs'                 THEN 'people-blogs'
       WHEN 'vlog'                  THEN 'people-blogs'
       WHEN 'vlogs'                 THEN 'people-blogs'
       WHEN 'entertainment'         THEN 'entertainment'
       WHEN 'autos-&-vehicles'      THEN 'autos'
       WHEN 'autos-and-vehicles'    THEN 'autos'
       WHEN 'autos'                 THEN 'autos'
       WHEN 'vehicles'              THEN 'autos'
       WHEN 'cars'                  THEN 'autos'
       WHEN 'documentary'           THEN 'documentary'
       WHEN 'documentaries'         THEN 'documentary'
       WHEN 'podcasts'              THEN 'podcasts'
       WHEN 'podcast'               THEN 'podcasts'
       WHEN 'kids'                  THEN 'kids'
       WHEN 'children'              THEN 'kids'
       WHEN 'education'             THEN 'education'
       WHEN 'gaming'                THEN 'gaming'
       WHEN 'games'                 THEN 'gaming'
       WHEN 'music'                 THEN 'music'
       WHEN 'news-&-politics'       THEN 'news'
       WHEN 'news-and-politics'     THEN 'news'
       WHEN 'news'                  THEN 'news'
       WHEN 'politics'              THEN 'news'
       WHEN 'pets-&-animals'        THEN 'pets'
       WHEN 'pets-and-animals'      THEN 'pets'
       WHEN 'pets'                  THEN 'pets'
       WHEN 'animals'               THEN 'pets'
       WHEN 'sports'                THEN 'sports'
       WHEN 'sport'                 THEN 'sports'
       WHEN 'travel-&-events'       THEN 'travel'
       WHEN 'travel-and-events'     THEN 'travel'
       WHEN 'travel'                THEN 'travel'
       WHEN 'events'                THEN 'travel'
       WHEN 'comedy'                THEN 'comedy'
       WHEN 'nonprofits-&-activism' THEN 'other'
       WHEN 'nonprofits-and-activism' THEN 'other'
       WHEN 'nonprofits'            THEN 'other'
       WHEN 'activism'              THEN 'other'
       WHEN 'tech'                  THEN 'tech'
       WHEN 'dance'                 THEN 'dance'
       WHEN 'food'                  THEN 'food'
       WHEN 'beauty'                THEN 'beauty'
       WHEN 'fashion'               THEN 'fashion'
       WHEN 'fitness'               THEN 'fitness'
       WHEN 'art'                   THEN 'art'
       WHEN 'lifestyle'             THEN 'lifestyle'
       WHEN 'business'              THEN 'business'
       WHEN 'other'                 THEN 'other'
       ELSE 'other'
   END
 WHERE content_type = 'long_video'
   AND category IS NOT NULL
   AND category <> ''
   AND category NOT IN (
       'comedy','music','dance','food','travel','sports','education','tech','beauty',
       'fashion','gaming','fitness','pets','art','news','lifestyle','business','other',
       'film-animation','science-tech','howto-style','people-blogs','entertainment',
       'autos','documentary','podcasts','kids');

-- ── B. Live -> video ────────────────────────────────────────────────────────
ALTER TABLE posts ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'upload';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS live_stream_id UUID;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'posts_source_check') THEN
        ALTER TABLE posts ADD CONSTRAINT posts_source_check CHECK (source IN ('upload', 'live'));
    END IF;
END $$;
-- One VOD post per stream: the consumer's INSERT is idempotent on redelivery.
CREATE UNIQUE INDEX IF NOT EXISTS uq_posts_live_stream
    ON posts (live_stream_id) WHERE live_stream_id IS NOT NULL;
-- The creator summary's `live` count and the Creator Hub's live filter.
CREATE INDEX IF NOT EXISTS idx_posts_author_source_live
    ON posts (author_id, created_at DESC) WHERE source = 'live' AND deleted_at IS NULL;

-- ── C. Channel branding ─────────────────────────────────────────────────────
-- contact_email already exists on a database user-service created (NOT NULL
-- DEFAULT ''); IF NOT EXISTS keeps both boot orders on one shape. The Go
-- side treats '' and NULL alike.
ALTER TABLE channels ADD COLUMN IF NOT EXISTS banner_media_id UUID;
ALTER TABLE channels ADD COLUMN IF NOT EXISTS links JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE channels ADD COLUMN IF NOT EXISTS contact_email TEXT;
ALTER TABLE channels ADD COLUMN IF NOT EXISTS featured_post_id UUID;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'channels_links_is_array') THEN
        ALTER TABLE channels ADD CONSTRAINT channels_links_is_array
            CHECK (jsonb_typeof(links) = 'array' AND jsonb_array_length(links) <= 10);
    END IF;
END $$;

-- ── D. System collections ───────────────────────────────────────────────────
ALTER TABLE playlists ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'user';
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'playlists_kind_check') THEN
        ALTER TABLE playlists ADD CONSTRAINT playlists_kind_check
            CHECK (kind IN ('user', 'watch_later', 'liked'));
    END IF;
END $$;
-- The owner column is creator_id (migration 012); "(owner_id, kind)" in the
-- contract is this index. ON CONFLICT on it makes create-on-first-read race-safe.
CREATE UNIQUE INDEX IF NOT EXISTS uq_playlists_system_kind
    ON playlists (creator_id, kind) WHERE kind <> 'user';
-- The channel page's public collection count.
CREATE INDEX IF NOT EXISTS idx_playlists_creator_public
    ON playlists (creator_id) WHERE visibility = 'public' AND kind = 'user';

-- ── E. Creator comment tools ────────────────────────────────────────────────
ALTER TABLE comments ADD COLUMN IF NOT EXISTS pinned_at  TIMESTAMPTZ;
ALTER TABLE comments ADD COLUMN IF NOT EXISTS hearted_at TIMESTAMPTZ;
-- One pinned comment per post, enforced by the database rather than by the
-- pin transaction alone.
CREATE UNIQUE INDEX IF NOT EXISTS uq_comments_pinned_per_post
    ON comments (post_id) WHERE pinned_at IS NOT NULL;
-- The creator inbox pages the author's own posts' comments newest-first and
-- asks per row "did the author reply"; idx_comments_post / idx_comments_parent
-- (setup.sql) already cover both sides of that join.

-- ── F. Post edit audit ──────────────────────────────────────────────────────
-- One append-only row per owner edit. `changes` is {field: {from, to}} for
-- every scalar that changed; `text` is recorded as {"changed": true} only,
-- so the audit carries no post body (same rule as post_admin_audit: no
-- content copied). No FK to posts: the audit must outlive a purge.
CREATE TABLE IF NOT EXISTS post_edit_audit (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id         UUID NOT NULL,
    actor_user_id   UUID NOT NULL,
    action          TEXT NOT NULL CHECK (action IN ('post.edit', 'post.bulk_visibility')),
    changes         JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_post_edit_audit_post
    ON post_edit_audit (post_id, created_at DESC);

CREATE OR REPLACE FUNCTION post_edit_audit_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'post_edit_audit is append-only';
END;
$$;

DROP TRIGGER IF EXISTS trg_post_edit_audit_append_only ON post_edit_audit;
CREATE TRIGGER trg_post_edit_audit_append_only
    BEFORE UPDATE OR DELETE ON post_edit_audit
    FOR EACH ROW EXECUTE FUNCTION post_edit_audit_append_only();
