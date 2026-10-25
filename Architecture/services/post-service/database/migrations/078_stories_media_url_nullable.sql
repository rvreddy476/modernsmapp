-- 078: stories.media_url is no longer required.
--
-- 032 moved stories to a canonical media_id and retired the caller-supplied
-- media_url (the create route no longer accepts one; story_handlers.go), and
-- CreateStoryPending (store/postgres/story_create.go) inserts media_id and no
-- media_url. But 002 created media_url TEXT NOT NULL and nothing relaxed it,
-- so on the real migration chain every story create failed with 23502
-- (null value in column "media_url"). The integration suite missed it while
-- it built its own stories table with a nullable media_url; on the shared
-- CI database (the full chain) it fails.
--
-- Expand-only: dropping NOT NULL admits the new rows and changes nothing for
-- the legacy ones, which keep their URL (032 holds them in manual_review).
-- stories_media_or_retired_chk (032) still requires a media_id on every row
-- that is not a retired legacy row.

ALTER TABLE stories ALTER COLUMN media_url DROP NOT NULL;
