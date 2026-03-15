-- Word blocklist for group content moderation
CREATE TABLE IF NOT EXISTS group_word_blocklist (
    group_id UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    word     TEXT NOT NULL,
    added_by UUID NOT NULL,
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, word)
);

-- Post approval queue (for groups with post_approval_required=TRUE)
CREATE TABLE IF NOT EXISTS post_approval_queue (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id    UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    post_id     UUID NOT NULL,
    author_id   UUID NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    reviewed_by UUID,
    reviewed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_approval_queue_group ON post_approval_queue(group_id, status, created_at DESC);

-- Groups auto-moderation flags
ALTER TABLE groups ADD COLUMN IF NOT EXISTS post_approval_required BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS auto_mod_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- Group sub-channels (forum-style)
CREATE TABLE IF NOT EXISTS group_channels (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id    UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    name        VARCHAR(100) NOT NULL,
    type        TEXT NOT NULL CHECK (type IN ('discussion','announcements','qa','resources','voice')),
    description TEXT NOT NULL DEFAULT '',
    is_archived BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order  INT NOT NULL DEFAULT 0,
    post_count  BIGINT NOT NULL DEFAULT 0,
    created_by  UUID NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_group_channels_group ON group_channels(group_id) WHERE is_archived = FALSE;

CREATE TABLE IF NOT EXISTS group_channel_posts (
    channel_id UUID NOT NULL REFERENCES group_channels(id) ON DELETE CASCADE,
    post_id    UUID NOT NULL,
    author_id  UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_id, post_id)
);
CREATE INDEX IF NOT EXISTS idx_channel_posts_time ON group_channel_posts(channel_id, created_at DESC);

-- Community Wiki
CREATE TABLE IF NOT EXISTS group_wiki_pages (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id   UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    title      TEXT NOT NULL,
    content    TEXT NOT NULL,
    created_by UUID NOT NULL,
    updated_by UUID,
    version    INT NOT NULL DEFAULT 1,
    is_pinned  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_wiki_group ON group_wiki_pages(group_id, is_pinned DESC);

-- Group Resources (links/files/documents)
CREATE TABLE IF NOT EXISTS group_resources (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id   UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    title      TEXT NOT NULL,
    type       TEXT NOT NULL CHECK (type IN ('link','file','document')),
    url        TEXT NOT NULL,
    media_id   UUID,
    added_by   UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
