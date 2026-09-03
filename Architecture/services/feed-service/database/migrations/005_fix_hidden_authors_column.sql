-- Migration 005: a stale `hidden_authors` table from an earlier, abandoned
-- attempt at this same feature already existed in some environments' data
-- volumes with column `user_id` instead of `author_id`. Because migration
-- 004 uses CREATE TABLE IF NOT EXISTS, it silently no-op'd against that
-- stale table on those environments, and every SetUserHidden/GetHiddenAuthors
-- call (internal/store/postgres/purge.go, written against `author_id`) then
-- failed at runtime with "column author_id does not exist".
--
-- Idempotent: on a fresh install (table already has author_id from 004)
-- this is a no-op; on an affected install it renames the column in place,
-- preserving any rows.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'hidden_authors' AND column_name = 'user_id'
    ) AND NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'hidden_authors' AND column_name = 'author_id'
    ) THEN
        ALTER TABLE hidden_authors RENAME COLUMN user_id TO author_id;
    END IF;
END $$;
