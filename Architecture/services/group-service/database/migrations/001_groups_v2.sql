-- 001_groups_v2.sql: Groups V2 schema migration
-- Adds handle, privacy tiers, join modes, join requests, group rules

BEGIN;

-- 1. Extend groups table with v2 columns
ALTER TABLE groups ADD COLUMN IF NOT EXISTS handle TEXT;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS category TEXT NOT NULL DEFAULT '';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS privacy_level TEXT NOT NULL DEFAULT 'public';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS join_mode TEXT NOT NULL DEFAULT 'open';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS who_can_post TEXT NOT NULL DEFAULT 'all_members';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS who_can_invite TEXT NOT NULL DEFAULT 'all_members';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS location TEXT NOT NULL DEFAULT '';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS language TEXT NOT NULL DEFAULT '';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

-- Backfill handle from id for existing rows (lowercase uuid)
UPDATE groups SET handle = LOWER(REPLACE(id::TEXT, '-', '')) WHERE handle IS NULL;

-- Backfill privacy_level from visibility
UPDATE groups SET privacy_level = visibility WHERE privacy_level = 'public' AND visibility = 'private';

-- Backfill status from is_archived
UPDATE groups SET status = 'archived' WHERE is_archived = TRUE AND status = 'active';

-- Constraints
ALTER TABLE groups ADD CONSTRAINT groups_handle_unique UNIQUE (handle);
ALTER TABLE groups ADD CONSTRAINT groups_privacy_level_check
    CHECK (privacy_level IN ('public', 'restricted', 'private'));
ALTER TABLE groups ADD CONSTRAINT groups_join_mode_check
    CHECK (join_mode IN ('open', 'request', 'invite_only'));
ALTER TABLE groups ADD CONSTRAINT groups_who_can_post_check
    CHECK (who_can_post IN ('all_members', 'admins_mods', 'admins_only'));
ALTER TABLE groups ADD CONSTRAINT groups_who_can_invite_check
    CHECK (who_can_invite IN ('all_members', 'admins_mods', 'admins_only'));
ALTER TABLE groups ADD CONSTRAINT groups_status_check
    CHECK (status IN ('active', 'archived', 'deleted'));

-- Indexes
CREATE INDEX IF NOT EXISTS idx_groups_handle ON groups (handle) WHERE status != 'deleted';
CREATE INDEX IF NOT EXISTS idx_groups_status ON groups (status) WHERE status != 'deleted';
CREATE INDEX IF NOT EXISTS idx_groups_category ON groups (category) WHERE category != '' AND status != 'deleted';

-- 2. Extend group_members with v2 columns
ALTER TABLE group_members ADD COLUMN IF NOT EXISTS id UUID DEFAULT gen_random_uuid();
ALTER TABLE group_members ADD COLUMN IF NOT EXISTS invited_by_user_id UUID;
ALTER TABLE group_members ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE group_members ADD COLUMN IF NOT EXISTS removed_at TIMESTAMPTZ;
ALTER TABLE group_members ADD COLUMN IF NOT EXISTS removed_by_user_id UUID;
ALTER TABLE group_members ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE group_members ADD CONSTRAINT group_members_status_check
    CHECK (status IN ('active', 'left', 'removed', 'banned'));

CREATE UNIQUE INDEX IF NOT EXISTS idx_group_members_id ON group_members (id);

-- 3. Extend group_invites with expires_at
ALTER TABLE group_invites ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;

-- 4. Create group_join_requests table
CREATE TABLE IF NOT EXISTS group_join_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    reviewed_by_user_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_join_requests_group_status
    ON group_join_requests (group_id, status) WHERE status = 'pending';
CREATE UNIQUE INDEX IF NOT EXISTS idx_join_requests_group_user_pending
    ON group_join_requests (group_id, user_id) WHERE status = 'pending';

-- 5. Create group_rules table
CREATE TABLE IF NOT EXISTS group_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    rule_order INT NOT NULL DEFAULT 0,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_group_rules_group ON group_rules (group_id, rule_order);

COMMIT;
