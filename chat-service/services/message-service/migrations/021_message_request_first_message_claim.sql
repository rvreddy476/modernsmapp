-- One first message per message request (chat lane review, 7 Nov 2026).
-- Additive and idempotent; database/setup.sql carries the same block (applied
-- on every boot by BootstrapSchema).
--
-- chat.message_requests
--   first_message_key  idempotency key of the send that claimed the request's
--                      single first message. ReserveMessageDeliveryIntent
--                      claims it with one conditional UPDATE in the same
--                      transaction as the delivery intent, so parallel sends
--                      with different keys can no longer each land a message
--                      (the old rule read preview = '' and wrote it after the
--                      message was stored: check-then-act).
--
-- Live-table safety: nullable, so ADD COLUMN is a catalog-only change; the
-- ALTER runs only while the column is missing, with lock_timeout 5 s for that
-- statement only (the 019 rule: a stuck ALTER fails the boot instead of
-- queueing every request write behind it).
DO $$
DECLARE
    prev_lock_timeout TEXT := current_setting('lock_timeout');
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'chat' AND table_name = 'message_requests' AND column_name = 'first_message_key'
    ) THEN
        PERFORM set_config('lock_timeout', '5s', true);
        ALTER TABLE chat.message_requests
            ADD COLUMN IF NOT EXISTS first_message_key TEXT;
        PERFORM set_config('lock_timeout', prev_lock_timeout, true);
    END IF;
END $$;
