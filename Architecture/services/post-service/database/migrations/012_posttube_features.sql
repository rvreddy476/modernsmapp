-- Migration 012: PostTube features
-- Video Series, Playlists, Chapters, End Screens, Cards, Watch Progress, Access control, Premieres

-- Video Series
CREATE TABLE IF NOT EXISTS video_series (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel_id      UUID REFERENCES channels(id),
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    cover_media_id  UUID REFERENCES media_assets(id),
    trailer_post_id UUID REFERENCES posts(id),
    episode_count   INT NOT NULL DEFAULT 0,
    is_complete     BOOLEAN NOT NULL DEFAULT FALSE,
    is_public       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_video_series_creator ON video_series(creator_id);

CREATE TABLE IF NOT EXISTS video_series_episodes (
    series_id   UUID NOT NULL REFERENCES video_series(id) ON DELETE CASCADE,
    post_id     UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    episode_num INT NOT NULL,
    title       TEXT,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (series_id, episode_num)
);
CREATE INDEX IF NOT EXISTS idx_video_series_episodes_post ON video_series_episodes(post_id);

-- Playlists
CREATE TABLE IF NOT EXISTS playlists (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel_id  UUID REFERENCES channels(id),
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    cover_url   TEXT,
    visibility  TEXT NOT NULL DEFAULT 'public' CHECK (visibility IN ('public','unlisted','private')),
    item_count  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_playlists_creator ON playlists(creator_id);

CREATE TABLE IF NOT EXISTS playlist_items (
    playlist_id UUID NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    post_id     UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    position    INT NOT NULL,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (playlist_id, position)
);
CREATE INDEX IF NOT EXISTS idx_playlist_items_post ON playlist_items(post_id);

-- Video Chapters
CREATE TABLE IF NOT EXISTS media_chapters (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id       UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    chapter_index INT NOT NULL,
    title         TEXT NOT NULL,
    start_ms      INT NOT NULL,
    thumbnail_url TEXT,
    source        TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual','ai_generated')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (post_id, chapter_index)
);

-- End Screens & Cards
CREATE TABLE IF NOT EXISTS video_end_screens (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id    UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    type       TEXT NOT NULL CHECK (type IN ('video','playlist','channel_subscribe','external_link')),
    target_id  UUID,
    target_url TEXT,
    title      TEXT,
    position   JSONB NOT NULL,
    start_ms   INT NOT NULL,
    end_ms     INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_video_end_screens_post ON video_end_screens(post_id);

CREATE TABLE IF NOT EXISTS video_cards (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id      UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    type         TEXT NOT NULL CHECK (type IN ('video','playlist','poll','external_link')),
    target_id    UUID,
    target_url   TEXT,
    title        TEXT NOT NULL,
    teaser_text  TEXT,
    appear_at_ms INT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_video_cards_post ON video_cards(post_id);

-- Watch Progress
CREATE TABLE IF NOT EXISTS watch_progress (
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    post_id         UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    position_ms     INT NOT NULL DEFAULT 0,
    duration_ms     INT NOT NULL,
    percent_watched REAL NOT NULL DEFAULT 0,
    completed       BOOLEAN NOT NULL DEFAULT FALSE,
    last_watched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, post_id)
);
CREATE INDEX IF NOT EXISTS idx_watch_progress_user ON watch_progress(user_id, last_watched_at DESC);

-- Membership-only videos
ALTER TABLE posts ADD COLUMN IF NOT EXISTS access TEXT NOT NULL DEFAULT 'public'
    CHECK (access IN ('public','subscribers_only','tier_specific'));
ALTER TABLE posts ADD COLUMN IF NOT EXISTS required_tier_id UUID REFERENCES creator_tiers(id);

-- Premieres
ALTER TABLE posts ADD COLUMN IF NOT EXISTS premiere_at TIMESTAMPTZ;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS premiere_reminder_count INT NOT NULL DEFAULT 0;
