-- How a member joined a group (founder, 22 Oct 2026). Additive and
-- idempotent; database/setup.sql carries the same statements (applied on
-- every boot by BootstrapSchema).
--
-- A group newcomer sees only messages sent after they join (cleared_before
-- is stamped with the join moment by AddMemberCapped). Their client shows a
-- WhatsApp-style note at the top of the chat saying how they joined, built
-- from these two columns:
--   join_method  'invite_link' | 'added' | 'invitation' | 'created'
--   added_by     who added them / whose invitation they accepted (NULL when
--                unknown, e.g. the group-service mirror carries no actor)
--
-- No backfill: existing rows stay NULL and get no note.
ALTER TABLE chat.conversation_members ADD COLUMN IF NOT EXISTS join_method TEXT;
ALTER TABLE chat.conversation_members ADD COLUMN IF NOT EXISTS added_by UUID;
