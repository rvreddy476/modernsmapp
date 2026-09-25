-- Emoji reactions on group posts: one current reaction per viewer per post.
--
-- The existing spark row IS the reaction row. group_post_sparks is already
-- unique on (post_id, user_id), which is exactly "one reaction per viewer per
-- post", so a reaction is a column on that row rather than a second table
-- that could disagree with it. Every legacy heart becomes a 'like' by the
-- column default — that is what a heart meant — and nothing is double
-- counted: spark_count keeps its legacy meaning (weighted hearts, a
-- supernova counting 5) and per-reaction counts are computed from these rows
-- at read time, never stored.
--
-- The allowlist lives in TWO places on purpose and a test asserts they agree:
-- this CHECK, so no writer can store a value the product does not know, and
-- service.ReactionAllowlist, so the API refuses before touching the row.
-- Adding a reaction is a migration plus a constant, deliberately.
ALTER TABLE group_post_sparks ADD COLUMN IF NOT EXISTS reaction TEXT NOT NULL DEFAULT 'like';
ALTER TABLE group_post_sparks ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'group_post_sparks_reaction_check'
    ) THEN
        ALTER TABLE group_post_sparks
            ADD CONSTRAINT group_post_sparks_reaction_check
            CHECK (reaction IN ('like', 'love', 'smile', 'wow', 'sad', 'angry'));
    END IF;
END $$;

-- Per-reaction tallies are a GROUP BY over (post_id, reaction) for a page of
-- posts; the index makes that an index-only scan.
CREATE INDEX IF NOT EXISTS idx_group_post_sparks_post_reaction
    ON group_post_sparks (post_id, reaction);
