-- Chat parity fix pass 2 (18 Oct 2026, lane M). Additive and idempotent;
-- database/setup.sql carries the same statement (applied on every boot by
-- BootstrapSchema).

-- PUT /v1/chat/conversations/:id/unread: a member may mark a conversation
-- unread for themselves. The inbox then reports has_unread = true and
-- unread_count >= 1 until the member next reads it (POST /read clears the
-- mark in the same statement that resets unread_count) or clears the mark.
ALTER TABLE chat.conversation_members
    ADD COLUMN IF NOT EXISTS marked_unread BOOLEAN NOT NULL DEFAULT FALSE;
