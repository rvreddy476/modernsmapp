-- Community posts
CREATE TABLE IF NOT EXISTS community_posts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id    UUID NOT NULL,
    space_id        UUID NOT NULL,
    author_id       TEXT NOT NULL,
    content_type    VARCHAR(20) NOT NULL DEFAULT 'text',
    title           VARCHAR(300),
    body            TEXT,
    body_html       TEXT,
    type_payload    JSONB NOT NULL DEFAULT '{}',
    attachments     JSONB NOT NULL DEFAULT '[]',
    tags            TEXT[] DEFAULT '{}',
    parent_post_id  UUID,
    thread_depth    INTEGER NOT NULL DEFAULT 0,
    reply_count     INTEGER NOT NULL DEFAULT 0,
    needs_approval  BOOLEAN NOT NULL DEFAULT FALSE,
    approved_by     TEXT,
    is_pinned       BOOLEAN NOT NULL DEFAULT FALSE,
    is_announcement BOOLEAN NOT NULL DEFAULT FALSE,
    is_featured     BOOLEAN NOT NULL DEFAULT FALSE,
    is_answered     BOOLEAN NOT NULL DEFAULT FALSE,
    accepted_answer_id UUID,
    is_expert_answer BOOLEAN NOT NULL DEFAULT FALSE,
    status          VARCHAR(20) NOT NULL DEFAULT 'published',
    spark_count     INTEGER NOT NULL DEFAULT 0,
    comment_count   INTEGER NOT NULL DEFAULT 0,
    echo_count      INTEGER NOT NULL DEFAULT 0,
    view_count      INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_community_posts_space ON community_posts(space_id, status, created_at DESC) WHERE status = 'published';
CREATE INDEX IF NOT EXISTS idx_community_posts_featured ON community_posts(community_id, is_featured) WHERE is_featured = TRUE;
CREATE INDEX IF NOT EXISTS idx_community_posts_thread ON community_posts(parent_post_id, created_at);
CREATE INDEX IF NOT EXISTS idx_community_posts_qa ON community_posts(space_id, is_answered) WHERE content_type = 'qa_question';

-- Wiki pages
CREATE TABLE IF NOT EXISTS community_wiki_pages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id    UUID NOT NULL,
    title           TEXT NOT NULL,
    slug            VARCHAR(200) NOT NULL,
    content         TEXT NOT NULL,
    content_html    TEXT,
    category        VARCHAR(50),
    is_pinned       BOOLEAN NOT NULL DEFAULT FALSE,
    created_by      TEXT NOT NULL,
    updated_by      TEXT,
    version         INTEGER NOT NULL DEFAULT 1,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(community_id, slug)
);

-- Bans
CREATE TABLE IF NOT EXISTS community_bans (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id    UUID NOT NULL,
    user_id         TEXT NOT NULL,
    banned_by       TEXT NOT NULL,
    reason          TEXT,
    expires_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(community_id, user_id)
);

-- Reports
CREATE TABLE IF NOT EXISTS community_reports (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id    UUID NOT NULL,
    reporter_id     TEXT NOT NULL,
    target_type     VARCHAR(20) NOT NULL,
    target_id       UUID NOT NULL,
    reason          VARCHAR(50) NOT NULL,
    description     TEXT,
    status          VARCHAR(20) NOT NULL DEFAULT 'pending',
    reviewed_by     TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_community_reports_pending ON community_reports(community_id, status) WHERE status = 'pending';

-- Engagement tables
CREATE TABLE IF NOT EXISTS community_post_sparks (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, is_supernova BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(), PRIMARY KEY (post_id, user_id)
);
CREATE TABLE IF NOT EXISTS community_post_stashes (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);
CREATE TABLE IF NOT EXISTS community_post_views (
    post_id UUID NOT NULL, user_id TEXT NOT NULL, viewed_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (post_id, user_id)
);

-- Add missing columns
ALTER TABLE communities ADD COLUMN IF NOT EXISTS tagline VARCHAR(200);
ALTER TABLE communities ADD COLUMN IF NOT EXISTS city VARCHAR(100);
ALTER TABLE communities ADD COLUMN IF NOT EXISTS region VARCHAR(100);
ALTER TABLE communities ADD COLUMN IF NOT EXISTS country VARCHAR(3);
ALTER TABLE communities ADD COLUMN IF NOT EXISTS welcome_message TEXT;
ALTER TABLE communities ADD COLUMN IF NOT EXISTS verified_type VARCHAR(20);
ALTER TABLE communities ADD COLUMN IF NOT EXISTS active_today INTEGER NOT NULL DEFAULT 0;
ALTER TABLE communities ADD COLUMN IF NOT EXISTS post_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE community_members ADD COLUMN IF NOT EXISTS role_title VARCHAR(50);
ALTER TABLE community_members ADD COLUMN IF NOT EXISTS notify_pref VARCHAR(20) NOT NULL DEFAULT 'all';
ALTER TABLE community_members ADD COLUMN IF NOT EXISTS intro_posted BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE community_members ADD COLUMN IF NOT EXISTS last_active_at TIMESTAMPTZ;
ALTER TABLE community_members ADD COLUMN IF NOT EXISTS invited_by TEXT;

ALTER TABLE community_spaces ADD COLUMN IF NOT EXISTS icon VARCHAR(10);
ALTER TABLE community_spaces ADD COLUMN IF NOT EXISTS visibility VARCHAR(20) NOT NULL DEFAULT 'all_members';
ALTER TABLE community_spaces ADD COLUMN IF NOT EXISTS who_can_post VARCHAR(20) NOT NULL DEFAULT 'all_members';
ALTER TABLE community_spaces ADD COLUMN IF NOT EXISTS who_can_reply VARCHAR(20) NOT NULL DEFAULT 'all_members';
ALTER TABLE community_spaces ADD COLUMN IF NOT EXISTS post_approval BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE community_spaces ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE community_spaces ADD COLUMN IF NOT EXISTS post_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE community_spaces ADD COLUMN IF NOT EXISTS member_count INTEGER NOT NULL DEFAULT 0;
