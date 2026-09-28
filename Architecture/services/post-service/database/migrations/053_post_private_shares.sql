-- Migration 053: private sharing (Creator Hub part 6b, 2026-09-28).
--
-- A 'private' post is readable by its owner and by the users on this list —
-- on the direct read, playback authorisation and the comments read only. The
-- list never widens a feed, search, channel list, Up next or notification:
-- those keep excluding every private post for everyone but the owner.
--
--   GET /v1/posts/:id/private-shares   owner only
--   PUT /v1/posts/:id/private-shares   owner only; replaces the list (<= 50)
--
-- The rows go with the post (ON DELETE CASCADE) and with a purged user
-- (users FK when the app users table exists, and PurgeUser deletes them
-- explicitly either way). Every replace writes one 'post.private_shares'
-- post_edit_audit row.

CREATE TABLE IF NOT EXISTS post_private_shares (
    post_id  UUID        NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    user_id  UUID        NOT NULL,
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);

-- "May this viewer read this post" is keyed (post_id, user_id) by the PK;
-- the purge and "shared with me" lookups go by user.
CREATE INDEX IF NOT EXISTS idx_post_private_shares_user ON post_private_shares (user_id);

DO $$
BEGIN
    IF to_regclass('public.users') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1 FROM pg_constraint
           WHERE conname = 'post_private_shares_user_id_fkey'
             AND conrelid = 'post_private_shares'::regclass) THEN
        ALTER TABLE post_private_shares
            ADD CONSTRAINT post_private_shares_user_id_fkey
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
    END IF;
END $$;

-- The same list 052 writes (see there).
ALTER TABLE post_edit_audit DROP CONSTRAINT IF EXISTS post_edit_audit_action_check;
ALTER TABLE post_edit_audit ADD CONSTRAINT post_edit_audit_action_check
    CHECK (action IN ('post.edit', 'post.bulk_visibility', 'post.bulk_edit', 'post.private_shares'));
