-- H7: CHECK constraint on posts.visibility. Today visibility values are
-- validated in the HTTP handler (`binding:"oneof=public followers private
-- unlisted"`) and by service-level guards, but a direct DB write or a
-- new code path could land any string. Push the constraint to the DB
-- as defence-in-depth.
--
-- Accepted values are the union of:
--   handler.go             — public, followers, private, unlisted
--   stories                — public, followers, close_friends
--   service-level auto     — trusted (after-hours protection)
--
-- DROP-IF-EXISTS then ADD makes this safely re-runnable. The runner
-- also gates on schema_migrations, so this only fires once per env
-- anyway — the IF EXISTS is purely defensive.

ALTER TABLE posts DROP CONSTRAINT IF EXISTS posts_visibility_check;
ALTER TABLE posts ADD CONSTRAINT posts_visibility_check
    CHECK (visibility IN ('public', 'followers', 'private', 'unlisted', 'trusted', 'close_friends'));
