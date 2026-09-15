-- Dating lane D4: tell dating-service when a dating-match conversation gets
-- its first message, so the match stops expiring.
--
-- One row per dating conversation, written by message delivery with
-- ON CONFLICT (conversation_id) DO NOTHING: the first delivered message wins
-- and every later message is a no-op. A background worker claims due rows
-- (FOR UPDATE SKIP LOCKED + lease), calls
-- POST /v1/dating/internal/matches/{match_id}/first-message and records the
-- outcome: delivered_at on 2xx, terminal_at on a permanent refusal
-- (400/404/409/410/422), otherwise next_attempt_at moves out by a capped
-- backoff. The send path never waits on dating-service.
--
-- Mirrored in database/setup.sql (applied on every boot).
CREATE TABLE IF NOT EXISTS chat.dating_first_message_notifications (
    conversation_id UUID PRIMARY KEY REFERENCES chat.conversations(id) ON DELETE CASCADE,
    match_id        UUID NOT NULL,
    actor_id        UUID NOT NULL,
    message_id      UUID NOT NULL,
    attempt_count   INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_status     INT,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at    TIMESTAMPTZ,
    terminal_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_dating_first_message_due
    ON chat.dating_first_message_notifications(next_attempt_at)
    WHERE delivered_at IS NULL AND terminal_at IS NULL;
