-- Memories service schema
CREATE SCHEMA IF NOT EXISTS memories;

-- User-created memory collections (albums)
CREATE TABLE IF NOT EXISTS memories.collections (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    title       VARCHAR(200) NOT NULL,
    description TEXT DEFAULT '',
    cover_url   TEXT,
    visibility  VARCHAR(20) NOT NULL DEFAULT 'private' CHECK (visibility IN ('public', 'friends', 'private')),
    item_count  INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mc_user ON memories.collections (user_id, created_at DESC);

-- Items in a memory collection
CREATE TABLE IF NOT EXISTS memories.collection_items (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    collection_id UUID NOT NULL REFERENCES memories.collections(id) ON DELETE CASCADE,
    post_id       UUID,
    media_url     TEXT,
    caption       TEXT DEFAULT '',
    sort_order    INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mci_collection ON memories.collection_items (collection_id, sort_order);

-- On This Day cache: pre-computed daily memories for each user
CREATE TABLE IF NOT EXISTS memories.on_this_day (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL,
    memory_date   DATE NOT NULL,
    post_id       UUID NOT NULL,
    years_ago     INTEGER NOT NULL,
    snippet       TEXT DEFAULT '',
    media_url     TEXT,
    generated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_otd_user_date_post ON memories.on_this_day (user_id, memory_date, post_id);
CREATE INDEX IF NOT EXISTS idx_otd_user_date ON memories.on_this_day (user_id, memory_date);

-- User preferences for memories (hide years, opt-out people, etc.)
CREATE TABLE IF NOT EXISTS memories.preferences (
    user_id             UUID PRIMARY KEY,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    hidden_years        INTEGER[] DEFAULT '{}',
    hidden_people_ids   UUID[] DEFAULT '{}',
    notification_time   TIME DEFAULT '09:00',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
