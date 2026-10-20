-- Disappearing messages (20 Oct 2026). Additive and idempotent;
-- database/setup.sql carries the same statements (applied on every boot by
-- BootstrapSchema).
--
-- 1. The timer is CONVERSATION-WIDE (WhatsApp semantics): one value every
--    member sees, set by either side of a DM or by a group owner/admin. The
--    older per-member chat.conversation_settings.disappear_after_ms column is
--    no longer authoritative (it was never enforced) and is left in place.
--    NULL = off. Only 24 h, 7 d and 90 d (in milliseconds) are allowed.
ALTER TABLE chat.conversations
    ADD COLUMN IF NOT EXISTS disappear_after_ms   BIGINT,
    ADD COLUMN IF NOT EXISTS disappear_updated_by UUID,
    ADD COLUMN IF NOT EXISTS disappear_updated_at TIMESTAMPTZ;
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'conversations_disappear_after_ms_allowed'
    ) THEN
        ALTER TABLE chat.conversations
            ADD CONSTRAINT conversations_disappear_after_ms_allowed
            CHECK (disappear_after_ms IS NULL OR disappear_after_ms IN (86400000, 604800000, 7776000000));
    END IF;
END $$;

-- 2. One row per message sent while the timer was on. expires_at is fixed
--    at send time (created_at + the timer then in force): changing or turning
--    off the timer later never moves it. The expiry worker claims due rows
--    with FOR UPDATE SKIP LOCKED (next_attempt_at doubles as the lease), so
--    it never scans Scylla. deleted_at records that the Postgres side of the
--    delete-for-everyone already ran, so a retry never releases an unread
--    count twice. The row is removed once the message is fully gone.
CREATE TABLE IF NOT EXISTS chat.message_expiries (
    message_id      UUID PRIMARY KEY,
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    bucket          TEXT NOT NULL,
    message_ts      TIMESTAMPTZ NOT NULL,
    sender_id       UUID NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    attempt_count   INT NOT NULL DEFAULT 0,
    deleted_at      TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_message_expiries_due
    ON chat.message_expiries(next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_message_expiries_conversation
    ON chat.message_expiries(conversation_id);
