-- Communities
CREATE TABLE IF NOT EXISTS communities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id UUID NOT NULL,
    handle TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    avatar_media_id UUID,
    banner_media_id UUID,
    community_type TEXT NOT NULL DEFAULT 'public' CHECK (community_type IN ('public','private','invite','education','local','professional','fan','brand')),
    category TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT '',
    join_mode TEXT NOT NULL DEFAULT 'open' CHECK (join_mode IN ('open','request','invite_only','email_domain')),
    email_domain_gate TEXT,
    join_questions JSONB DEFAULT '[]',
    member_directory BOOLEAN NOT NULL DEFAULT true,
    cross_space_bans BOOLEAN NOT NULL DEFAULT true,
    max_sub_spaces INT NOT NULL DEFAULT 50,
    latitude DOUBLE PRECISION,
    longitude DOUBLE PRECISION,
    location_name TEXT NOT NULL DEFAULT '',
    rules TEXT[] NOT NULL DEFAULT '{}',
    topic_tags TEXT[] NOT NULL DEFAULT '{}',
    member_count BIGINT NOT NULL DEFAULT 0,
    space_count INT NOT NULL DEFAULT 0,
    is_verified BOOLEAN NOT NULL DEFAULT false,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','archived','deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_comm_owner ON communities(owner_id);
CREATE INDEX IF NOT EXISTS idx_comm_handle ON communities(handle) WHERE status != 'deleted';
CREATE INDEX IF NOT EXISTS idx_comm_type ON communities(community_type) WHERE status != 'deleted';
CREATE INDEX IF NOT EXISTS idx_comm_tags ON communities USING gin(topic_tags) WHERE status != 'deleted';

-- Community members
CREATE TABLE IF NOT EXISTS community_members (
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner','admin','moderator','space_manager','expert','member','pending','suspended','banned')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    banned_at TIMESTAMPTZ,
    banned_by UUID,
    ban_reason TEXT,
    PRIMARY KEY (community_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_community_members_user ON community_members(user_id);

-- Community spaces (linked groups/channels/etc)
CREATE TABLE IF NOT EXISTS community_spaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    space_type TEXT NOT NULL CHECK (space_type IN ('group','channel','discussion','events','resources')),
    linked_group_id UUID,
    linked_channel_id UUID,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    sort_order INT NOT NULL DEFAULT 0,
    is_quarantined BOOLEAN NOT NULL DEFAULT false,
    created_by UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_cs_community ON community_spaces(community_id);

-- Join requests
CREATE TABLE IF NOT EXISTS community_join_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    answers JSONB,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    reviewed_by UUID,
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(community_id, user_id, status)
);

-- Moderation log
CREATE TABLE IF NOT EXISTS community_modlog (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    actor_id UUID NOT NULL,
    action TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id UUID NOT NULL,
    reason TEXT,
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_modlog_comm ON community_modlog(community_id, created_at DESC);

-- Announcements
CREATE TABLE IF NOT EXISTS community_announcements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    author_id UUID NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    is_pinned BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Events
CREATE TABLE IF NOT EXISTS community_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    space_id UUID REFERENCES community_spaces(id) ON DELETE SET NULL,
    creator_id UUID NOT NULL,
    title TEXT NOT NULL,
    description TEXT DEFAULT '',
    location TEXT,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ,
    max_attendees INT DEFAULT 0,
    rsvp_count INT DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
