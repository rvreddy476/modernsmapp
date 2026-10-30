-- Online index builds (chat backend audit L9, M2, M8; 30 Oct 2026).
--
-- RUNS OUTSIDE A TRANSACTION, ONE STATEMENT AT A TIME. Do not paste this
-- file into database/setup.sql: BootstrapSchema sends setup.sql as one
-- multi-statement query, which PostgreSQL runs as one implicit transaction,
-- and CREATE INDEX CONCURRENTLY refuses to run inside a transaction block.
--
-- The statements below are applied by postgres.BuildConcurrentIndexes from
-- the list in database/concurrent_indexes.go (a unit test keeps that list and
-- this file identical). It runs in the background after boot, so a long build
-- never delays readiness, and:
--   * one replica builds at a time (session advisory lock);
--   * an index that already exists and is valid is skipped;
--   * an INVALID leftover of an interrupted CONCURRENTLY build is dropped
--     with DROP INDEX CONCURRENTLY and built again.
-- CONCURRENTLY takes SHARE UPDATE EXCLUSIVE, so inserts and updates (every
-- send's outbox row) keep flowing while an index builds. The queries these
-- indexes serve work without them, only slower, until the build finishes.
--
-- Convention from here on: every new index on chat.outbox_events,
-- chat.conversation_members, chat.message_delivery_intents (and any other
-- large, write-hot table) is added here and to database/concurrent_indexes.go,
-- never as a plain CREATE INDEX in setup.sql.

-- M2: the per-user cap on pending scheduled messages counts by sender.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_scheduled_msg_sender_pending ON chat.scheduled_messages (sender_id) WHERE status = 'pending';

-- M8: completed delivery intents whose member list has not been compacted
-- yet. Compaction empties member_ids, which takes the row out of this
-- partial index, so it only ever holds the recent tail.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_delivery_intents_uncompacted ON chat.message_delivery_intents (completed_at) WHERE completed_at IS NOT NULL AND member_ids <> '{}'::uuid[];
