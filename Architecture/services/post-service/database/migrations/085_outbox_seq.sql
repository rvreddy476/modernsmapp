-- Migration 085: insert order for outbox rows (feed-07 leftover).
--
-- The relay reads post_outbox_events oldest first, by created_at. created_at
-- is NOW(), which is the transaction's start time, so two events one
-- transaction writes for the same post (an edit that changes distribution
-- and visibility together) carry the same value and were ordered by their
-- random id. seq is a sequence value taken at INSERT: within a transaction
-- it rises in the order the rows were written, and the relay breaks a
-- created_at tie on it (store/postgres/outbox.go).
--
-- Safe on a live table and with the previous image running:
--   * the column is added nullable with no default, then given its default:
--     both are catalogue changes, no table rewrite and no scan. Rows written
--     before this migration keep seq NULL and sort ahead of numbered rows
--     with the same created_at, by id as before;
--   * an image that does not know the column inserts without naming it and
--     the default fills it in; nothing reads it but the relay's ORDER BY.
-- Locking: ACCESS EXCLUSIVE on post_outbox_events for the two ALTERs, held
-- until this file's transaction commits (milliseconds). While it waits for
-- that lock it queues behind any open transaction that touched the table and
-- holds up new inserts behind itself, so the wait is capped: after 5 s the
-- migration fails, the pod does not start and the next start tries again.
-- No index: the relay still finds its rows through idx_outbox_unpublished
-- (created_at) and only breaks ties on seq.
--
-- Idempotent.

SET LOCAL lock_timeout = '5s';

CREATE SEQUENCE IF NOT EXISTS post_outbox_events_seq;

ALTER TABLE post_outbox_events ADD COLUMN IF NOT EXISTS seq BIGINT;
ALTER TABLE post_outbox_events ALTER COLUMN seq SET DEFAULT nextval('post_outbox_events_seq');

ALTER SEQUENCE post_outbox_events_seq OWNED BY post_outbox_events.seq;
