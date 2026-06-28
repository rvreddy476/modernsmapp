CREATE TABLE IF NOT EXISTS broadcast_channels (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id UUID NOT NULL,
    handle TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    description TEXT DEFAULT '',
    avatar_media_id UUID,
    banner_media_id UUID,
    channel_type TEXT NOT NULL DEFAULT 'public' CHECK (channel_type IN ('public','private','creator','brand','education','official','topic','paid')),
    category TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT '',
    comment_mode TEXT NOT NULL DEFAULT 'enabled' CHECK (comment_mode IN ('enabled','moderated','subscribers_only','disabled')),
    reaction_mode TEXT NOT NULL DEFAULT 'enabled' CHECK (reaction_mode IN ('enabled','disabled')),
    forward_allowed BOOLEAN NOT NULL DEFAULT true,
    paid_access BOOLEAN NOT NULL DEFAULT false,
    subscription_price_cents INT NOT NULL DEFAULT 0,
    post_schedule_enabled BOOLEAN NOT NULL DEFAULT true,
    subscriber_count_visible BOOLEAN NOT NULL DEFAULT true,
    allow_preview_posts INT NOT NULL DEFAULT 3,
    is_verified BOOLEAN NOT NULL DEFAULT false,
    subscriber_count BIGINT NOT NULL DEFAULT 0,
    update_count BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended','archived','deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_bc_owner ON broadcast_channels(owner_id);
CREATE INDEX IF NOT EXISTS idx_bc_handle ON broadcast_channels(handle) WHERE status != 'deleted';
CREATE INDEX IF NOT EXISTS idx_bc_type ON broadcast_channels(channel_type) WHERE status != 'deleted';

CREATE TABLE IF NOT EXISTS channel_members (
    channel_id UUID NOT NULL REFERENCES broadcast_channels(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    role TEXT NOT NULL DEFAULT 'subscriber' CHECK (role IN ('owner','admin','editor','moderator','subscriber','banned')),
    notify_on TEXT NOT NULL DEFAULT 'all' CHECK (notify_on IN ('all','highlights','none')),
    muted_until TIMESTAMPTZ,
    snoozed_until TIMESTAMPTZ,
    paid BOOLEAN NOT NULL DEFAULT false,
    subscribed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_cm_user ON channel_members(user_id);

-- Self-heal: older deployments created channel_members before these columns
-- existed (CREATE TABLE IF NOT EXISTS won't add them). Idempotent ALTERs run on
-- every BootstrapSchema startup so existing DBs converge to the current schema.
-- (Missing notify_on caused subscribe/unsubscribe to 500: SQLSTATE 42703.)
ALTER TABLE channel_members ADD COLUMN IF NOT EXISTS notify_on TEXT NOT NULL DEFAULT 'all';
ALTER TABLE channel_members ADD COLUMN IF NOT EXISTS muted_until TIMESTAMPTZ;
ALTER TABLE channel_members ADD COLUMN IF NOT EXISTS snoozed_until TIMESTAMPTZ;
ALTER TABLE channel_members ADD COLUMN IF NOT EXISTS paid BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE channel_members ADD COLUMN IF NOT EXISTS subscribed_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE TABLE IF NOT EXISTS channel_updates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id UUID NOT NULL REFERENCES broadcast_channels(id) ON DELETE CASCADE,
    author_id UUID NOT NULL,
    update_type TEXT NOT NULL DEFAULT 'announcement' CHECK (update_type IN ('announcement','image','video','audio','poll','event','commerce','alert','digest')),
    title TEXT,
    body TEXT NOT NULL DEFAULT '',
    media_ids UUID[] NOT NULL DEFAULT '{}',
    metadata JSONB,
    is_pinned BOOLEAN NOT NULL DEFAULT false,
    scheduled_at TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','scheduled','published','deleted')),
    view_count BIGINT NOT NULL DEFAULT 0,
    reaction_count BIGINT NOT NULL DEFAULT 0,
    comment_count BIGINT NOT NULL DEFAULT 0,
    forward_count BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_cu_channel ON channel_updates(channel_id, published_at DESC) WHERE status = 'published';
CREATE INDEX IF NOT EXISTS idx_cu_scheduled ON channel_updates(scheduled_at) WHERE status = 'scheduled';

-- Add stash_count if missing (idempotent)
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='channel_updates' AND column_name='stash_count') THEN
        ALTER TABLE channel_updates ADD COLUMN stash_count BIGINT NOT NULL DEFAULT 0;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS update_sparks (
    update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    is_supernova BOOLEAN NOT NULL DEFAULT false,
    weight INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (update_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_us_user ON update_sparks(user_id);

CREATE TABLE IF NOT EXISTS update_views (
    update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (update_id, user_id)
);

CREATE TABLE IF NOT EXISTS update_stashes (
    update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (update_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_ust_user ON update_stashes(user_id);

CREATE TABLE IF NOT EXISTS update_echoes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    echo_type TEXT NOT NULL DEFAULT 'share',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ue_update_user ON update_echoes(update_id, user_id);
CREATE INDEX IF NOT EXISTS idx_ue_update ON update_echoes(update_id);
CREATE INDEX IF NOT EXISTS idx_ue_user ON update_echoes(user_id);

CREATE TABLE IF NOT EXISTS update_comments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
    author_id UUID NOT NULL,
    body TEXT NOT NULL,
    parent_id UUID REFERENCES update_comments(id) ON DELETE SET NULL,
    is_pinned BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_uc_update ON update_comments(update_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_uc_author ON update_comments(author_id);

CREATE TABLE IF NOT EXISTS poll_votes (
    update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    option_index INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (update_id, user_id, option_index)
);

CREATE TABLE IF NOT EXISTS event_rsvps (
    update_id UUID NOT NULL REFERENCES channel_updates(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('going','interested','not_going')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (update_id, user_id)
);
