-- Migration 004: Group types and additional settings per GCC spec

-- Group types per GCC spec
ALTER TABLE groups ADD COLUMN IF NOT EXISTS group_type TEXT NOT NULL DEFAULT 'public';
ALTER TABLE groups ADD CONSTRAINT groups_group_type_check
    CHECK (group_type IN ('public','private','hidden','local','study','marketplace','brand','event','family'));

-- Missing settings from spec
ALTER TABLE groups ADD COLUMN IF NOT EXISTS max_members INT NOT NULL DEFAULT 0;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS join_questions JSONB DEFAULT '[]';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS topic_tags TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS comment_permission TEXT NOT NULL DEFAULT 'all_members';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS member_list_visible BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS link_sharing BOOLEAN NOT NULL DEFAULT true;

ALTER TABLE groups ADD CONSTRAINT groups_comment_permission_check
    CHECK (comment_permission IN ('all_members','admins_mods','admins_only'));

-- Discovery indexes
CREATE INDEX IF NOT EXISTS idx_groups_type ON groups(group_type) WHERE status != 'deleted';
CREATE INDEX IF NOT EXISTS idx_groups_tags ON groups USING gin(topic_tags) WHERE status != 'deleted';
