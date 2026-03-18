-- Group posts
CREATE TABLE IF NOT EXISTS group_posts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
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

-- Group channels (sub-sections)
CREATE TABLE IF NOT EXISTS group_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
    name VARCHAR(100) NOT NULL,
    type VARCHAR(20) DEFAULT 'discussion',
    description TEXT DEFAULT '',
    who_can_post VARCHAR(20) DEFAULT 'all_members',
    is_default BOOLEAN DEFAULT FALSE,
    is_archived BOOLEAN DEFAULT FALSE,
    sort_order INTEGER DEFAULT 0,
    post_count BIGINT DEFAULT 0,
    created_by TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_group_channels_group ON group_channels(group_id, is_archived, sort_order);

-- Group events
CREATE TABLE IF NOT EXISTS group_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
    post_id UUID,
    creator_id TEXT NOT NULL,
    title VARCHAR(200) NOT NULL,
    description TEXT,
    cover_media_id UUID,
    start_at TIMESTAMPTZ NOT NULL,
    end_at TIMESTAMPTZ,
    timezone VARCHAR(50) DEFAULT 'UTC',
    is_all_day BOOLEAN DEFAULT FALSE,
    location_type VARCHAR(20) DEFAULT 'online',
    address TEXT,
    online_link TEXT,
    rsvp_enabled BOOLEAN DEFAULT TRUE,
    max_attendees INTEGER DEFAULT 0,
    going_count INTEGER DEFAULT 0,
    maybe_count INTEGER DEFAULT 0,
    status VARCHAR(20) DEFAULT 'upcoming',
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Group bans
CREATE TABLE IF NOT EXISTS group_bans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
    user_id TEXT NOT NULL,
    banned_by TEXT NOT NULL,
    reason TEXT,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(group_id, user_id)
);

-- Group reports
CREATE TABLE IF NOT EXISTS group_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
    reporter_id TEXT NOT NULL,
    target_type VARCHAR(20) NOT NULL,
    target_id UUID NOT NULL,
    reason VARCHAR(50) NOT NULL,
    description TEXT,
    status VARCHAR(20) DEFAULT 'pending',
    reviewed_by TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_group_reports_pending ON group_reports(group_id, status) WHERE status = 'pending';

-- Group invites
CREATE TABLE IF NOT EXISTS group_invites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
    inviter_id TEXT NOT NULL,
    invitee_id TEXT NOT NULL,
    status VARCHAR(20) DEFAULT 'pending',
    message VARCHAR(200),
    expires_at TIMESTAMPTZ DEFAULT NOW() + INTERVAL '7 days',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(group_id, invitee_id)
);

-- Group join requests
CREATE TABLE IF NOT EXISTS group_join_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id UUID NOT NULL,
    user_id TEXT NOT NULL,
    status VARCHAR(20) DEFAULT 'pending',
    answers JSONB DEFAULT '[]',
    reviewed_by TEXT,
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(group_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_join_requests_pending ON group_join_requests(group_id, status) WHERE status = 'pending';

-- Add missing columns to groups
ALTER TABLE groups ADD COLUMN IF NOT EXISTS active_today INTEGER DEFAULT 0;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS posting_permission VARCHAR(20) DEFAULT 'all_members';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS invite_only BOOLEAN DEFAULT FALSE;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS approval_required BOOLEAN DEFAULT FALSE;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS visibility VARCHAR(20) DEFAULT 'public';

-- Engagement tables
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
