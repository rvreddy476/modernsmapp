-- Migration 052: Creator Hub settings (2026-09-28).
--
-- Four owner-set columns the PATCH /v1/posts/:id edit sheet and the bulk
-- route write, and the post detail reads for every viewer:
--
--   age_restricted        18+ only: the detail and every playback read refuse
--                         anonymous (401) and under-18 / unknown-age (403)
--                         viewers; the owner always passes.
--   hide_like_count       the owner still sees the number; everyone else gets
--                         like_count null.
--   default_comment_sort  'top' | 'newest', the order the watch page opens
--                         comments in.
--   related_post_id       one of the owner's own posts to link from the watch
--                         page; SET NULL when that post is hard-deleted
--                         (a soft delete leaves it and the read hides it).
--
-- Every statement is guarded (IF NOT EXISTS / pg_constraint lookup) so a
-- re-run is a no-op; setup.sql carries the same columns for fresh boots.

ALTER TABLE posts ADD COLUMN IF NOT EXISTS age_restricted       BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS hide_like_count      BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS default_comment_sort TEXT    NOT NULL DEFAULT 'top';
ALTER TABLE posts ADD COLUMN IF NOT EXISTS related_post_id      UUID    NULL;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'posts_default_comment_sort_check') THEN
        ALTER TABLE posts ADD CONSTRAINT posts_default_comment_sort_check
            CHECK (default_comment_sort IN ('top', 'newest'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'posts_related_post_id_fkey') THEN
        ALTER TABLE posts ADD CONSTRAINT posts_related_post_id_fkey
            FOREIGN KEY (related_post_id) REFERENCES posts(id) ON DELETE SET NULL;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'posts_related_post_not_self') THEN
        ALTER TABLE posts ADD CONSTRAINT posts_related_post_not_self
            CHECK (related_post_id IS NULL OR related_post_id <> id);
    END IF;
END $$;

-- The SET NULL above scans posts by related_post_id on every hard delete.
CREATE INDEX IF NOT EXISTS idx_posts_related_post_id ON posts (related_post_id) WHERE related_post_id IS NOT NULL;

-- POST /v1/uploads/bulk now edits more than visibility: each post it writes
-- gets one 'post.bulk_edit' audit row (051's 'post.bulk_visibility' stays
-- valid for the rows already written). The list is the whole Creator Hub
-- set, 053's 'post.private_shares' included, and 053 writes the same list:
-- re-running either migration can never narrow the CHECK under rows the
-- other one allowed.
ALTER TABLE post_edit_audit DROP CONSTRAINT IF EXISTS post_edit_audit_action_check;
ALTER TABLE post_edit_audit ADD CONSTRAINT post_edit_audit_action_check
    CHECK (action IN ('post.edit', 'post.bulk_visibility', 'post.bulk_edit', 'post.private_shares'));
