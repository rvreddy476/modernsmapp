-- 007_close_friends_source.sql — friends-sheets spec §3.1.
-- Records how a Trusted Circle member was added so suggestion- and
-- mutual-driven adds can be told apart from manual ones. Idempotent.
ALTER TABLE close_friends
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'manual';

ALTER TABLE close_friends DROP CONSTRAINT IF EXISTS close_friends_source_check;
ALTER TABLE close_friends
    ADD CONSTRAINT close_friends_source_check
    CHECK (source IN ('manual', 'suggested', 'mutual_top'));
