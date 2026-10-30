-- Outbox relay claims and dead letters (commerce backend audit M4,
-- 30 Oct 2026). Columns read by shared/outbox.Publisher, which now claims
-- rows with a lease (FOR UPDATE SKIP LOCKED) under a per-table advisory lock,
-- so replicas no longer publish the same row twice, and dead-letters a row
-- that can never be published instead of retrying it at the head forever.
--
--   claimed_until  lease of the relay that claimed the row; also the back-off
--                  of a row the broker refused.
--   attempts       counted failed publishes (the broker refused the record).
--                  A Kafka outage is not counted and never dead-letters.
--   last_error     why the last publish failed (error text, never the payload).
--   failed_at      dead letter: skipped by the relay, kept for an operator.
--                  Requeue: UPDATE payments.outbox_events SET failed_at = NULL, attempts = 0
--                  WHERE id = ...;
--
-- Live-table safety: every column is nullable or has a constant default, so
-- ADD COLUMN is catalog-only (no rewrite). The ALTER runs only when a column
-- is missing, and then with lock_timeout 5s, so an ALTER queued behind a long
-- transaction fails the boot (the pod retries, old pods keep serving) instead
-- of stalling every outbox INSERT behind it. No new index: the existing
-- partial index on unpublished rows serves the claim.
--
-- Rolling deploy: old pods ignore these columns (they still read
-- published_at IS NULL and may double-publish until drained, as before); new
-- pods fall back to a lock-only legacy relay if the columns are absent.
DO $$
DECLARE
    prev_lock_timeout TEXT := current_setting('lock_timeout');
BEGIN
    IF to_regclass('payments.outbox_events') IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM pg_attribute
        WHERE attrelid = to_regclass('payments.outbox_events')
          AND attname = 'failed_at' AND NOT attisdropped
    ) THEN
        PERFORM set_config('lock_timeout', '5s', true);
        ALTER TABLE payments.outbox_events
            ADD COLUMN IF NOT EXISTS claimed_until TIMESTAMPTZ,
            ADD COLUMN IF NOT EXISTS attempts      INT NOT NULL DEFAULT 0,
            ADD COLUMN IF NOT EXISTS last_error    TEXT,
            ADD COLUMN IF NOT EXISTS failed_at     TIMESTAMPTZ;
        PERFORM set_config('lock_timeout', prev_lock_timeout, true);
    END IF;
END $$;
