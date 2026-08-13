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
--
-- DROP-IF-EXISTS FIRST. This is not defensive noise; without it a fresh
-- database can never finish bootstrapping.
--
-- setup.sql runs before the migrations and already creates chk_content_type
-- (with the modern 8-value taxonomy, see setup.sql "Idempotent: drop-if-exists
-- then re-add"). A bare ADD CONSTRAINT here then fails with SQLSTATE 42710
-- "constraint already exists", migrationrunner aborts the whole bootstrap,
-- post-service exits, and it fails identically on every restart — so a NEW
-- environment (staging, prod, DR) could never bring post-service up at all.
-- Existing environments are unaffected because they applied 003 historically,
-- before setup.sql gained the constraint, which is why this never showed up
-- in a warm database.
--
-- Migration 021 and setup.sql already use exactly this pattern; 003 was the
-- one that did not. Re-adding the narrow 4-value set here is correct: the
-- migrations run in order and 021 widens it again to the current taxonomy.
ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_content_type;

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
