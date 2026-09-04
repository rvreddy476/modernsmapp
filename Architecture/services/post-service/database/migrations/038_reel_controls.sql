-- Per-reel controls from the Momentum reel composer (founder decision, 2026-09-04).
--
-- A reel carries four switches: allow comments (`no_comments`, existing),
-- allow remix (`remix_setting`, existing), and the two added here. Plus the
-- people tagged in it.
--
-- `allow_download` defaults TRUE and `hide_share` defaults FALSE because the
-- absence of a decision must mean "the post behaves as every post always has".
-- Every row that predates this migration was created by a composer that never
-- offered these switches; flipping their behaviour retroactively would be a
-- silent product change nobody asked for.
--
-- `tagged_user_ids` is NOT NULL DEFAULT '{}' rather than nullable like
-- `mentions`: "nobody tagged" and "unknown" are the same state here, and a
-- single representation means readers never have to reason about NULL.
--
-- No index on `tagged_user_ids` yet. Nothing queries "posts I am tagged in" in
-- this pass, and `migrationrunner` runs each file inside a transaction, so a
-- concurrent GIN build would have to come from an operational job anyway.

ALTER TABLE posts ADD COLUMN IF NOT EXISTS hide_share      BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS allow_download  BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS tagged_user_ids UUID[]  NOT NULL DEFAULT '{}';
