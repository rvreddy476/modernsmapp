CREATE TABLE IF NOT EXISTS groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    avatar_media_id UUID,
    cover_media_id UUID,
    creator_id UUID NOT NULL,
    visibility TEXT NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private')),
    is_archived BOOLEAN NOT NULL DEFAULT FALSE,
    chat_conversation_id UUID,
    member_count BIGINT DEFAULT 0,
    post_count BIGINT DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_groups_creator ON groups(creator_id);
CREATE INDEX IF NOT EXISTS idx_groups_visibility ON groups(visibility) WHERE is_archived = FALSE;
CREATE INDEX IF NOT EXISTS idx_groups_name_search ON groups USING gin(to_tsvector('english', name));

CREATE TABLE IF NOT EXISTS group_creation_requests (
    creator_id UUID NOT NULL,
    idempotency_key TEXT NOT NULL,
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (creator_id, idempotency_key)
);

CREATE TABLE IF NOT EXISTS group_members (
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'moderator', 'member')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_group_members_user ON group_members(user_id);

CREATE TABLE IF NOT EXISTS group_invites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    inviter_id UUID NOT NULL,
    invitee_id UUID NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(group_id, invitee_id)
);
CREATE INDEX IF NOT EXISTS idx_group_invites_invitee ON group_invites(invitee_id, status);

CREATE TABLE IF NOT EXISTS group_posts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    post_id UUID,
    channel_id UUID,
    author_id TEXT NOT NULL,
    content_type VARCHAR(20) DEFAULT 'text',
    title VARCHAR(200),
    body TEXT,
    body_html TEXT,
    type_payload JSONB DEFAULT '{}',
    attachments JSONB DEFAULT '[]',
    needs_approval BOOLEAN DEFAULT FALSE,
    approved_by TEXT,
    approved_at TIMESTAMPTZ,
    is_pinned BOOLEAN DEFAULT FALSE,
    is_announcement BOOLEAN DEFAULT FALSE,
    status VARCHAR(20) DEFAULT 'published',
    spark_count INTEGER DEFAULT 0,
    comment_count INTEGER DEFAULT 0,
    echo_count INTEGER DEFAULT 0,
    view_count INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_group_posts_feed ON group_posts(group_id, status, created_at DESC) WHERE status = 'published';
CREATE INDEX IF NOT EXISTS idx_group_posts_pinned ON group_posts(group_id, is_pinned) WHERE is_pinned = TRUE;
CREATE INDEX IF NOT EXISTS idx_group_posts_pending ON group_posts(group_id, status) WHERE status = 'pending_approval';
CREATE INDEX IF NOT EXISTS idx_group_posts_group_time ON group_posts(group_id, created_at DESC);

CREATE TABLE IF NOT EXISTS group_post_sparks (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, is_supernova BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(), PRIMARY KEY (post_id, user_id)
);
CREATE TABLE IF NOT EXISTS group_post_comments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id UUID NOT NULL, user_id TEXT NOT NULL, body TEXT NOT NULL,
    parent_id UUID, is_pinned BOOLEAN DEFAULT FALSE, spark_count INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_group_comments_post ON group_post_comments(post_id, created_at DESC);
CREATE TABLE IF NOT EXISTS group_post_stashes (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);
CREATE TABLE IF NOT EXISTS group_post_views (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, viewed_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);
CREATE TABLE IF NOT EXISTS group_post_echoes (
    id UUID PRIMARY KEY,
    post_id UUID NOT NULL,
    user_id TEXT NOT NULL,
    echo_type VARCHAR(20) NOT NULL DEFAULT 'share',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(post_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL, post_id UUID, creator_id TEXT NOT NULL,
    title VARCHAR(200) NOT NULL, description TEXT, cover_media_id UUID,
    start_at TIMESTAMPTZ NOT NULL, end_at TIMESTAMPTZ,
    timezone VARCHAR(50) DEFAULT 'UTC', is_all_day BOOLEAN DEFAULT FALSE,
    location_type VARCHAR(20) DEFAULT 'online', address TEXT, online_link TEXT,
    rsvp_enabled BOOLEAN DEFAULT TRUE, max_attendees INTEGER DEFAULT 0,
    going_count INTEGER DEFAULT 0, maybe_count INTEGER DEFAULT 0,
    status VARCHAR(20) DEFAULT 'upcoming', created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS group_event_rsvps (
    event_id UUID NOT NULL, user_id TEXT NOT NULL,
    status VARCHAR(20) NOT NULL CHECK (status IN ('going', 'maybe', 'not_going')),
    created_at TIMESTAMPTZ DEFAULT NOW(), PRIMARY KEY (event_id, user_id)
);

CREATE TABLE IF NOT EXISTS group_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL, name VARCHAR(100) NOT NULL,
    type VARCHAR(20) DEFAULT 'discussion', description TEXT DEFAULT '',
    who_can_post VARCHAR(20) DEFAULT 'all_members',
    is_default BOOLEAN DEFAULT FALSE, is_archived BOOLEAN DEFAULT FALSE,
    sort_order INTEGER DEFAULT 0, post_count BIGINT DEFAULT 0,
    created_by TEXT NOT NULL, created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_group_channels_group ON group_channels(group_id, is_archived, sort_order);

CREATE TABLE IF NOT EXISTS group_wiki_pages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL, title VARCHAR(200) NOT NULL, content TEXT NOT NULL,
    created_by TEXT NOT NULL, updated_by TEXT,
    version INTEGER DEFAULT 1, is_pinned BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS group_bans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL, user_id TEXT NOT NULL, banned_by TEXT NOT NULL,
    reason TEXT, expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(), UNIQUE(group_id, user_id)
);

CREATE TABLE IF NOT EXISTS post_approval_queue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL, post_id UUID NOT NULL, author_id TEXT NOT NULL,
    status VARCHAR(20) DEFAULT 'pending', reviewed_by TEXT, reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_approval_queue_pending ON post_approval_queue(group_id, status) WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS member_stats (
    group_id UUID NOT NULL, user_id UUID NOT NULL,
    post_count INTEGER DEFAULT 0, sparks_received INTEGER DEFAULT 0,
    updated_at TIMESTAMPTZ DEFAULT NOW(), PRIMARY KEY (group_id, user_id)
);
