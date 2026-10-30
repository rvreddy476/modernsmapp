-- Outbox relay claims and dead letters; scheduled-message leases (chat backend
-- audit H3, H4, M2; 30 Oct 2026). Additive and idempotent; database/setup.sql
-- carries the same block (applied on every boot by BootstrapSchema).
--
-- Live-table safety:
--   * Every new column is nullable or has a constant default, so on
--     PostgreSQL 11+ ADD COLUMN is a catalog-only change: no table rewrite.
--   * The ALTERs run only when a column is missing (checked first), so a
--     normal boot takes no ACCESS EXCLUSIVE lock on these tables at all.
--   * When they do run, lock_timeout is 5 s for those statements only. An
--     ALTER stuck behind a long transaction would otherwise queue every send's
--     outbox INSERT behind it; with the timeout the boot fails instead, the
--     pod restarts and retries, and the old pods keep serving.
--
-- chat.outbox_events
--   claimed_until  lease of the relay that claimed the row (FOR UPDATE SKIP
--                  LOCKED), so replicas never publish the same row twice;
--                  also the back-off of a row whose publish failed.
--   attempts       failed publish attempts.
--   last_error     why the last attempt failed (error text only, never the
--                  payload).
--   failed_at      dead letter: the row can never be published (too large, or
--                  OutboxMaxAttempts counted failures). The relay skips it, so
--                  one poison row no longer blocks the head of the queue.
--                  Requeue: UPDATE chat.outbox_events SET failed_at = NULL,
--                  attempts = 0 WHERE id = ...;
--
-- chat.scheduled_messages
--   claimed_until  lease of the scheduler replica sending the row.
--
-- New indexes for these tables are NOT built here: this file runs inside the
-- boot transaction, where CREATE INDEX CONCURRENTLY is not allowed. They are
-- in 020_concurrent_indexes.sql (audit L9).
DO $$
DECLARE
    prev_lock_timeout TEXT := current_setting('lock_timeout');
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'chat' AND table_name = 'outbox_events' AND column_name = 'failed_at'
    ) THEN
        PERFORM set_config('lock_timeout', '5s', true);
        ALTER TABLE chat.outbox_events
            ADD COLUMN IF NOT EXISTS claimed_until TIMESTAMPTZ,
            ADD COLUMN IF NOT EXISTS attempts      INT NOT NULL DEFAULT 0,
            ADD COLUMN IF NOT EXISTS last_error    TEXT,
            ADD COLUMN IF NOT EXISTS failed_at     TIMESTAMPTZ;
        PERFORM set_config('lock_timeout', prev_lock_timeout, true);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'chat' AND table_name = 'scheduled_messages' AND column_name = 'claimed_until'
    ) THEN
        PERFORM set_config('lock_timeout', '5s', true);
        ALTER TABLE chat.scheduled_messages
            ADD COLUMN IF NOT EXISTS claimed_until TIMESTAMPTZ;
        PERFORM set_config('lock_timeout', prev_lock_timeout, true);
    END IF;
END $$;
