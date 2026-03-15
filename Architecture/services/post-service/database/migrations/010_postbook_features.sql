-- Quote Echo: ref_post_id FK
ALTER TABLE posts ADD COLUMN IF NOT EXISTS ref_post_id UUID REFERENCES posts(id) ON DELETE SET NULL;

-- Scheduled posts
ALTER TABLE posts ADD COLUMN IF NOT EXISTS publish_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_posts_scheduled ON posts(publish_at) WHERE status = 'scheduled';

-- Supernova weight on reactions
ALTER TABLE reactions ADD COLUMN IF NOT EXISTS weight SMALLINT NOT NULL DEFAULT 1;

-- Update reaction type constraint to include atpost vocabulary
ALTER TABLE reactions DROP CONSTRAINT IF EXISTS reactions_reaction_type_check;
ALTER TABLE reactions ADD CONSTRAINT reactions_reaction_type_check
    CHECK (reaction_type IN ('spark','supernova','tune','love','haha','wow','sad','angry'));

-- Supernova count on engagement counts
ALTER TABLE post_engagement_counts ADD COLUMN IF NOT EXISTS supernova_count INT NOT NULL DEFAULT 0;

-- Tune (private negative signal)
CREATE TABLE IF NOT EXISTS tunes (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    post_id    UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, post_id)
);
CREATE INDEX IF NOT EXISTS idx_tunes_user ON tunes(user_id, created_at DESC);

-- Link previews cache
CREATE TABLE IF NOT EXISTS link_previews (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    url         TEXT NOT NULL UNIQUE,
    title       TEXT,
    description TEXT,
    image_url   TEXT,
    domain      TEXT,
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours'
);
CREATE INDEX IF NOT EXISTS idx_link_previews_url ON link_previews(url);
CREATE INDEX IF NOT EXISTS idx_link_previews_expiry ON link_previews(expires_at);
ALTER TABLE posts ADD COLUMN IF NOT EXISTS link_preview_id UUID REFERENCES link_previews(id);

-- Article / Longform
ALTER TABLE posts ADD COLUMN IF NOT EXISTS article_content JSONB;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS reading_time_minutes INT;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS is_paywalled BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS article_tags (
    post_id UUID NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    tag     TEXT NOT NULL,
    PRIMARY KEY (post_id, tag)
);
CREATE INDEX IF NOT EXISTS idx_article_tags ON article_tags(tag);

-- Story interactive elements
CREATE TABLE IF NOT EXISTS story_interactive (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    story_id   UUID NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
    type       TEXT NOT NULL CHECK (type IN ('poll','quiz','countdown','question','slider')),
    question   TEXT NOT NULL,
    options    JSONB,
    correct_idx INT,
    end_time   TIMESTAMPTZ,
    position   JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS story_interactive_responses (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    interactive_id UUID NOT NULL REFERENCES story_interactive(id) ON DELETE CASCADE,
    user_id        UUID NOT NULL,
    response       JSONB NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (interactive_id, user_id)
);

-- Events
CREATE TABLE IF NOT EXISTS events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id         UUID REFERENCES posts(id) ON DELETE CASCADE,
    creator_id      UUID NOT NULL REFERENCES users(id),
    title           TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    starts_at       TIMESTAMPTZ NOT NULL,
    ends_at         TIMESTAMPTZ,
    location_name   TEXT,
    location_lat    DOUBLE PRECISION,
    location_lng    DOUBLE PRECISION,
    cover_media_id  UUID,
    is_ticketed     BOOLEAN NOT NULL DEFAULT FALSE,
    ticket_price    NUMERIC(10,2),
    max_attendees   INT,
    chat_conv_id    UUID,
    rsvp_count      INT NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'upcoming' CHECK (status IN ('upcoming','ongoing','ended','cancelled')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_events_starts ON events(starts_at) WHERE status = 'upcoming';
CREATE INDEX IF NOT EXISTS idx_events_location ON events(location_lat, location_lng) WHERE location_lat IS NOT NULL;

CREATE TABLE IF NOT EXISTS event_rsvps (
    event_id   UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id),
    status     TEXT NOT NULL DEFAULT 'going' CHECK (status IN ('going','maybe','not_going')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (event_id, user_id)
);
