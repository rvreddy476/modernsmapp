-- 004_conversation_member_left_at.sql
-- Block-sever support (messaging/privacy spec v2 §16.1).
--
-- When user A blocks user B, A is severed from their shared direct
-- conversation: the conversation disappears from A's inbox and A can no
-- longer send into it. We model the sever non-destructively with a
-- left_at timestamp on the membership row so the conversation history
-- and the other party's view are preserved.

ALTER TABLE chat.conversation_members
    ADD COLUMN IF NOT EXISTS left_at TIMESTAMPTZ;
