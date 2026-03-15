-- Audio Library: which audio track is used in a post
ALTER TABLE posts ADD COLUMN IF NOT EXISTS audio_id UUID;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS audio_start_ms INT DEFAULT 0;

-- Remix / Duet
ALTER TABLE posts ADD COLUMN IF NOT EXISTS remix_source_id UUID REFERENCES posts(id) ON DELETE SET NULL;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS remix_type TEXT CHECK (remix_type IN ('duet','stitch','reaction'));
ALTER TABLE posts ADD COLUMN IF NOT EXISTS remix_layout TEXT CHECK (remix_layout IN ('side_by_side','top_bottom','react_cam'));
ALTER TABLE posts ADD COLUMN IF NOT EXISTS allow_remix BOOLEAN NOT NULL DEFAULT TRUE;

-- Remix count on engagement
ALTER TABLE post_engagement_counts ADD COLUMN IF NOT EXISTS remix_count INT NOT NULL DEFAULT 0;

-- Music rights check (before publish gate)
CREATE TABLE IF NOT EXISTS media_rights_checks (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id       UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    audio_id      UUID,
    check_type    TEXT NOT NULL CHECK (check_type IN ('audio_rights','content_id','copyright')),
    status        TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','cleared','flagged','manual_review')),
    provider      TEXT,
    provider_ref  TEXT,
    result_detail JSONB,
    checked_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rights_checks_post ON media_rights_checks(post_id, status);

-- Flick Series
CREATE TABLE IF NOT EXISTS flick_series (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    cover_url     TEXT,
    episode_count INT NOT NULL DEFAULT 0,
    is_complete   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_flick_series_creator ON flick_series(creator_id);

CREATE TABLE IF NOT EXISTS flick_series_items (
    series_id   UUID NOT NULL REFERENCES flick_series(id) ON DELETE CASCADE,
    post_id     UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    episode_num INT NOT NULL,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (series_id, episode_num)
);
CREATE INDEX IF NOT EXISTS idx_series_items_post ON flick_series_items(post_id);

CREATE TABLE IF NOT EXISTS flick_series_followers (
    series_id   UUID NOT NULL REFERENCES flick_series(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    followed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (series_id, user_id)
);
