-- Dating mechanic M9 (in-match extras).
--
-- chat.conversations.dating_receipts_gated: in this dating conversation a
--   member sees the other's read receipts only while their
--   dating_receipts_until is in the future (set by dating-service to the
--   member's pass expiry while they opt in).
-- chat.conversations.dating_call_after_exchange: the open-match answer that
--   graph-service grants calls on also requires both members to have sent a
--   message.
-- chat.conversation_members.first_sent_at: the member's first delivered
--   message in a dating conversation.
-- Both conversation flags default off, so every existing conversation is
-- unchanged; dating-service switches them on when it creates one.
--
-- Mirrored in database/setup.sql (applied on every boot).
ALTER TABLE chat.conversations ADD COLUMN IF NOT EXISTS dating_receipts_gated BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE chat.conversations ADD COLUMN IF NOT EXISTS dating_call_after_exchange BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE chat.conversation_members ADD COLUMN IF NOT EXISTS first_sent_at TIMESTAMPTZ;
ALTER TABLE chat.conversation_members ADD COLUMN IF NOT EXISTS dating_receipts_until TIMESTAMPTZ;
