-- Delivered ticks (founder, 22 Oct 2026). Additive and idempotent;
-- database/setup.sql carries the same statements (applied on every boot by
-- BootstrapSchema).
--
-- WhatsApp-style ticks for the sender: one grey tick = sent, two grey =
-- delivered (the recipient's device received it; in a group, every other
-- member's), two coloured = read. Read already has a cursor
-- (chat.read_cursors). This is the matching DELIVERED cursor: one row per
-- member per conversation, advanced when the member's client acknowledges
-- what it has received (POST /v1/chat/conversations/:id/delivered).
--
-- last_delivered_at is the server ts of the newest message the device has
-- (capped at the ack time), so a message sent after it is never marked
-- delivered by an earlier ack. Monotonic: the upsert never moves it back.
-- No per-message rows: a sender's tick is ts <= every other member's cursor.
CREATE TABLE IF NOT EXISTS chat.delivered_cursors (
    conversation_id           UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    user_id                   UUID NOT NULL,
    last_delivered_message_id UUID,
    last_delivered_at         TIMESTAMPTZ NOT NULL,
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_delivered_cursors_user ON chat.delivered_cursors(user_id);
