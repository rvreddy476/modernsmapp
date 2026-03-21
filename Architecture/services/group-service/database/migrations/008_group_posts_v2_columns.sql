-- ============================================================
-- Migration 008: Upgrade group_posts from V1 to V2 schema
-- ============================================================
-- V1 schema: (group_id, post_id, author_id, created_at) PK(group_id, post_id)
-- V2 schema: id UUID PK, group_id, author_id TEXT, body, title, counters, etc.
--
-- V1 "post_id" was an FK to a separate posts table (post-service).
-- V2 stores content natively in group_posts (body, title, attachments).
-- We keep post_id as nullable for backward compat with V1 rows.

-- Step 1: Add all V2 columns
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS id UUID DEFAULT gen_random_uuid();
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS channel_id UUID;
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS content_type VARCHAR(20) DEFAULT 'text';
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS title VARCHAR(200);
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS body TEXT;
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS body_html TEXT;
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS type_payload JSONB DEFAULT '{}';
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS attachments JSONB DEFAULT '[]';
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS needs_approval BOOLEAN DEFAULT FALSE;
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS approved_by TEXT;
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS approved_at TIMESTAMPTZ;
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS is_pinned BOOLEAN DEFAULT FALSE;
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS is_announcement BOOLEAN DEFAULT FALSE;
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'published';
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS spark_count INTEGER DEFAULT 0;
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS comment_count INTEGER DEFAULT 0;
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS echo_count INTEGER DEFAULT 0;
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS view_count INTEGER DEFAULT 0;
ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ DEFAULT NOW();

-- Step 2: Populate id for any existing rows that have NULL id
UPDATE group_posts SET id = gen_random_uuid() WHERE id IS NULL;

-- Step 3: Make id NOT NULL now that all rows have values
ALTER TABLE group_posts ALTER COLUMN id SET NOT NULL;

-- Step 4: Drop old composite PK and make post_id nullable
-- (V2 inserts don't use post_id; V1 rows have it populated)
ALTER TABLE group_posts DROP CONSTRAINT IF EXISTS group_posts_pkey;
ALTER TABLE group_posts ALTER COLUMN post_id DROP NOT NULL;

-- Step 5: Set id as the new PK
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'group_posts_pkey_v2'
  ) THEN
    ALTER TABLE group_posts ADD CONSTRAINT group_posts_pkey_v2 PRIMARY KEY (id);
  END IF;
END $$;

-- Step 6: Cast author_id from UUID to TEXT if needed (V1 used UUID, V2 uses TEXT)
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'group_posts' AND column_name = 'author_id' AND data_type = 'uuid'
  ) THEN
    ALTER TABLE group_posts ALTER COLUMN author_id TYPE TEXT USING author_id::TEXT;
  END IF;
END $$;

-- Step 7: Indexes
CREATE INDEX IF NOT EXISTS idx_group_posts_feed ON group_posts(group_id, status, created_at DESC) WHERE status = 'published';
CREATE INDEX IF NOT EXISTS idx_group_posts_pinned ON group_posts(group_id, is_pinned) WHERE is_pinned = TRUE;
CREATE INDEX IF NOT EXISTS idx_group_posts_pending ON group_posts(group_id, status) WHERE status = 'pending_approval';
CREATE INDEX IF NOT EXISTS idx_group_posts_group_id ON group_posts(group_id, created_at DESC);

-- Step 8: Engagement tables
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

-- Step 9: Events + RSVPs
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
CREATE INDEX IF NOT EXISTS idx_event_rsvps_event ON group_event_rsvps(event_id);

-- Step 10: Other supporting tables
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
