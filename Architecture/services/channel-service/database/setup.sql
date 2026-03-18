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
