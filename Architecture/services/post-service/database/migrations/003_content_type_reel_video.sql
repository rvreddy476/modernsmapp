-- Migration: backfill video posts with reel/video content_type and add CHECK constraint

-- Step 1: Backfill existing video posts based on media duration
UPDATE posts p
   SET content_type = CASE
       WHEN ma.duration_seconds IS NOT NULL AND ma.duration_seconds <= 90 THEN 'reel'
       ELSE 'video'
   END,
   updated_at = NOW()
FROM post_media pm
JOIN media_assets ma ON ma.id = pm.media_id
WHERE pm.post_id = p.id
  AND pm.kind = 'video'
  AND p.content_type = 'post'
  AND p.deleted_at IS NULL
  AND ma.duration_seconds IS NOT NULL;

-- Step 2: Enforce content_type enum
ALTER TABLE posts
    ADD CONSTRAINT chk_content_type
    CHECK (content_type IN ('post', 'poll', 'reel', 'video'));

-- Step 3: Partial indexes for reel/video profile tabs
CREATE INDEX IF NOT EXISTS idx_posts_author_reel
    ON posts (author_id, created_at DESC)
    WHERE content_type = 'reel' AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_posts_author_video
    ON posts (author_id, created_at DESC)
    WHERE content_type = 'video' AND deleted_at IS NULL;
