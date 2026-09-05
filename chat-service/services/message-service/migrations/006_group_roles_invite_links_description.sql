-- 006_group_roles_invite_links_description.sql
-- Chat groups hardening (2026-09-05 chat-app pass).
--
-- 1. conversation_members.role admits 'owner'. The service has written
--    'owner' since the production chat pass; the original CHECK only knew
--    ('admin','member'). setup.sql already swapped the constraint on boot;
--    this file is the standalone migration for deployments that apply the
--    numbered files instead of replaying setup.sql.
-- 2. conversations.description (<= 300 runes, enforced in the service).
-- 3. chat.group_invite_links — shareable join links for groups.
--
-- Every statement is idempotent.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint
               WHERE conname = 'conversation_members_role_check') THEN
        ALTER TABLE chat.conversation_members
            DROP CONSTRAINT conversation_members_role_check;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conname = 'conversation_members_role_check_v2') THEN
        ALTER TABLE chat.conversation_members
            ADD CONSTRAINT conversation_members_role_check_v2
            CHECK (role IN ('owner', 'admin', 'member'));
    END IF;
END $$;

ALTER TABLE chat.conversations
    ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';

-- One LIVE link per group (revoked_at IS NULL); creating a new one revokes
-- the previous. `code` is 10 chars of Crockford-free base32 (A-Z2-7),
-- generated from crypto/rand. Joins are counted in `uses`; max_uses NULL
-- means unlimited. expires_at NULL means never.
CREATE TABLE IF NOT EXISTS chat.group_invite_links (
    code            TEXT PRIMARY KEY,
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    created_by      UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ,
    max_uses        INT,
    uses            INT NOT NULL DEFAULT 0,
    revoked_at      TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_group_invite_links_live
    ON chat.group_invite_links(conversation_id) WHERE revoked_at IS NULL;
