-- Migration 082: Echo notes (docs/designs/echo-contract.md, "Storage").
--
-- A note is a `posts` row with content_type = 'note'. It is written only by
-- POST /v1/notes and POST /v1/notes/thread (service/notes_create.go); the
-- ordinary POST /v1/posts refuses the type.
--
-- 1. 'note' joins the ONE content_type constraint. Migration 044's rule:
--    drop and re-add chk_content_type under its own name, never a second
--    constraint beside it (two CHECKs intersect, they do not union). The
--    list is 044's union plus 'note'. setup.sql, which runs on every boot,
--    re-adds the same list so a restart cannot narrow it again.
--
-- 2. The conversation columns. reply_to_id is the note this one answers
--    (another author's or the author's own); conversation_id is the top
--    note of the conversation (its own id on a top-level note);
--    quote_of_id is the note a quote points at. reply_policy decides who
--    may reply; reply_count is the number of live direct replies;
--    edited_at is set by the 30-minute text edit. None of these mean
--    anything on a row that is not a note, and none is read for one.
--
-- 3. The three partial indexes from design section 5.2: direct replies in
--    order, a conversation in order, an author's notes newest first.
--
-- Idempotent throughout.

ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_posts_content_type;
ALTER TABLE posts DROP CONSTRAINT IF EXISTS posts_content_type_check;
ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_content_type;

ALTER TABLE posts ADD CONSTRAINT chk_content_type
    CHECK (content_type IN (
        -- ordinary posts
        'post', 'poll', 'voice',
        -- video, current vocabulary
        'flick', 'long_video',
        -- video, legacy synonyms kept because rows still carry them
        'reel', 'video',
        -- crosspost embeds, written by CreateCrosspostLink
        'video_embed', 'flick_embed',
        -- Echo short posts (2026-11-12)
        'note'
    ));

ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS reply_to_id     UUID,
    ADD COLUMN IF NOT EXISTS conversation_id UUID,
    ADD COLUMN IF NOT EXISTS reply_policy    TEXT NOT NULL DEFAULT 'everyone',
    ADD COLUMN IF NOT EXISTS reply_count     INT  NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS quote_of_id     UUID,
    ADD COLUMN IF NOT EXISTS edited_at       TIMESTAMPTZ;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_posts_reply_policy') THEN
        ALTER TABLE posts ADD CONSTRAINT chk_posts_reply_policy
            CHECK (reply_policy IN ('everyone', 'following', 'mentioned'));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_posts_note_replies
    ON posts (reply_to_id, created_at)
    WHERE content_type = 'note' AND reply_to_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_posts_note_conversation
    ON posts (conversation_id, created_at)
    WHERE content_type = 'note' AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_posts_note_author
    ON posts (author_id, created_at DESC)
    WHERE content_type = 'note' AND deleted_at IS NULL;
