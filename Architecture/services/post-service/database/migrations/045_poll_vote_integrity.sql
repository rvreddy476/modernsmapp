-- Migration 045: a poll vote must name an option OF the poll it is cast on.
--
-- ─── What was wrong ───────────────────────────────────────────────────
--
-- `poll_votes` carried exactly one constraint:
--
--     PRIMARY KEY (post_id, user_id, option_id)
--
-- and nothing else. No foreign key on option_id, in either direction.
-- That key stops one thing — the same user casting the same option on
-- the same post twice — and nothing else. In particular it does not
-- stop:
--
--   * an option_id belonging to a DIFFERENT poll, and
--   * an option_id that has never existed at all.
--
-- Both insert cleanly. Proven on the dev stack on 2026-09-09 by posting
-- {"option_id":"00000000-0000-4000-8000-00000000dead"} to
-- /v1/posts/{id}/poll/vote: 200 OK, a row landed, and the poll's
-- total_votes went from 3 to 4 while the two real options still summed
-- to 3. The percentages then read 50% and 25% — they no longer add up,
-- because GetPoll totalled every row in poll_votes for the post,
-- including the row that matched no option.
--
-- ─── The fix ──────────────────────────────────────────────────────────
--
-- A COMPOSITE foreign key, (option_id, post_id) → poll_options(id, post_id).
--
-- The composite form is the point. A plain FK on option_id → poll_options(id)
-- would reject the fabricated UUID but happily accept a real option id
-- borrowed from someone else's poll — the cross-poll case, which is the
-- one an attacker can actually construct, because those ids are public in
-- every poll payload. Matching post_id too is what makes "an option of
-- THIS poll" a thing the database itself knows.
--
-- Referencing (id, post_id) needs a unique constraint on that pair;
-- poll_options.id is already the primary key, so the extra UNIQUE is
-- redundant as a uniqueness statement and exists only to give the FK
-- something to point at. It costs one index.
--
-- ON DELETE CASCADE matches what both purge paths already do by hand
-- (store/postgres/purge.go and posts_lifecycle.go delete poll_votes
-- before poll_options); it means any path that forgets is still correct.
--
-- The service layer validates the same rule before inserting, so a normal
-- client gets POLL_OPTION_INVALID / 400 rather than a constraint error.
-- The constraint is the floor under that check, for every writer that is
-- not this code path: a backfill, another service, a psql session.
--
-- ─── Data repair ──────────────────────────────────────────────────────
--
-- Rows that violate the new constraint are votes for options that do not
-- exist. They cannot be displayed, attributed or corrected — GetPoll can
-- only count them into a total that then disagrees with its own parts.
-- They are deleted rather than preserved, and the constraint is added
-- VALIDATED so a later "why is this NOT VALID" is never a question.

-- 1. Something for the composite FK to reference.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'poll_options_id_post_id_key'
          AND conrelid = 'poll_options'::regclass
    ) THEN
        ALTER TABLE poll_options
            ADD CONSTRAINT poll_options_id_post_id_key UNIQUE (id, post_id);
    END IF;
END $$;

-- 2. Orphan votes: an option_id that is not an option of that post's poll.
DELETE FROM poll_votes v
WHERE NOT EXISTS (
    SELECT 1 FROM poll_options o
    WHERE o.id = v.option_id
      AND o.post_id = v.post_id
);

-- 3. The constraint itself.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'poll_votes_option_fkey'
          AND conrelid = 'poll_votes'::regclass
    ) THEN
        ALTER TABLE poll_votes
            ADD CONSTRAINT poll_votes_option_fkey
            FOREIGN KEY (option_id, post_id)
            REFERENCES poll_options (id, post_id)
            ON DELETE CASCADE;
    END IF;
END $$;
