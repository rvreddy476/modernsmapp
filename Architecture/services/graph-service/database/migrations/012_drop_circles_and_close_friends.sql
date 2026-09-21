-- 012: drop the second relationship tier and the unused audience tables.
--
-- WHY
--
-- The product had three names for two concepts: the code said
-- "connections", the web said "Circle" for connections, and "Trusted
-- Circle" meant close_friends. The founder collapsed this to one concept
-- on 21 Sep: Connections. These tables are what is left over.
--
--   close_friends        a second private tier ("Trusted Circle"). Gated
--                        the close_friends/trusted post and story audience.
--   circles              named custom audiences. Full CRUD existed; NOTHING
--   circle_members       ever called it — no web, no Android, no service.
--   relationship_labels  per-pair nicknames. Same: built, never consumed.
--   favorites            a starred-contacts list. Same.
--
-- SAFETY
--
-- Verified on dev before writing this: all five tables held ZERO rows, and
-- no post or story used the close_friends/trusted visibility (495 public,
-- 5 private, 2 followers, 1 unlisted). So nothing becomes invisible and,
-- more importantly, nothing becomes visible that was not before.
--
-- The audience resolvers fail CLOSED without this data: post-service's
-- thread and story policies treat an unrecognised or unsatisfied audience
-- as author-only, not public. If another environment still holds
-- close-friends rows, the effect of this migration is that those posts
-- become visible to their author only. That is the safe direction, and it
-- is why the visibility CHECK constraints are deliberately NOT narrowed
-- here — a row that still says 'close_friends' must keep parsing and
-- resolving to "no", rather than failing a constraint at read time.
--
-- BEFORE RUNNING THIS ON STAGING OR PRODUCTION, check:
--
--   SELECT count(*) FROM close_friends;
--   SELECT visibility, count(*) FROM posts
--    WHERE visibility IN ('close_friends','trusted') GROUP BY visibility;
--
-- A non-zero result is not a blocker, but it means real content changes
-- audience, so it needs a deliberate decision rather than a silent drop.

BEGIN;

DROP TABLE IF EXISTS circle_members;
DROP TABLE IF EXISTS circles;
DROP TABLE IF EXISTS relationship_labels;
DROP TABLE IF EXISTS favorites;
DROP TABLE IF EXISTS close_friends;

COMMIT;
