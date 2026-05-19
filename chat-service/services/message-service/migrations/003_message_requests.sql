-- 003_message_requests.sql
-- Dedicated message-request envelope (messaging/privacy spec v2 §8.6).
--
-- The conversation row already carries is_request=TRUE (from migration 002).
-- This table holds the request lifecycle: who sent it, the first-message
-- preview, status, risk score and expiry. One row per request conversation.

CREATE TABLE IF NOT EXISTS chat.message_requests (
    id              BIGSERIAL PRIMARY KEY,
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    sender_id       UUID NOT NULL,
    receiver_id     UUID NOT NULL,
    preview         VARCHAR(500) NOT NULL DEFAULT '',
    status          VARCHAR(16) NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','accepted','ignored','blocked','reported','expired')),
    risk_score      SMALLINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at    TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days')
);

-- One message_requests row per conversation.
CREATE UNIQUE INDEX IF NOT EXISTS idx_message_requests_conversation
    ON chat.message_requests(conversation_id);
-- Drives the recipient's Requests folder.
CREATE INDEX IF NOT EXISTS idx_message_requests_receiver_pending
    ON chat.message_requests(receiver_id, created_at DESC) WHERE status = 'pending';
