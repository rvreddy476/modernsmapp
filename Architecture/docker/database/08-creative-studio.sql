-- =============================================================================
-- Creative Studio — editor sessions, stickers, templates, overlays, exports
-- Run against: app (public schema)
-- =============================================================================

-- Editor sessions (auto-save drafts)
CREATE TABLE IF NOT EXISTS editor_sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    mode            TEXT NOT NULL CHECK (mode IN ('flick','story','carousel','long_video')),
    state_json      JSONB NOT NULL DEFAULT '{}',
    thumbnail_url   TEXT,
    last_saved_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_editor_sessions_user ON editor_sessions(user_id, last_saved_at DESC);

-- Sticker packs
CREATE TABLE IF NOT EXISTS sticker_packs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    category    TEXT NOT NULL CHECK (category IN ('trending','branded','emoji','seasonal','interactive')),
    cover_url   TEXT NOT NULL,
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS stickers (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pack_id          UUID NOT NULL REFERENCES sticker_packs(id) ON DELETE CASCADE,
    asset_url        TEXT NOT NULL,
    type             TEXT NOT NULL CHECK (type IN ('static','lottie','gif','interactive')),
    interactive_type TEXT CHECK (interactive_type IN ('poll','question','countdown','slider','mention','location','product','link')),
    tags             TEXT[] NOT NULL DEFAULT '{}',
    use_count        BIGINT NOT NULL DEFAULT 0,
    is_active        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_stickers_pack ON stickers(pack_id) WHERE is_active = TRUE;
CREATE INDEX IF NOT EXISTS idx_stickers_trending ON stickers(use_count DESC) WHERE is_active = TRUE;

-- Flick templates
CREATE TABLE IF NOT EXISTS flick_templates (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title         TEXT NOT NULL,
    category      TEXT NOT NULL,
    preview_url   TEXT NOT NULL,
    cover_url     TEXT NOT NULL,
    template_json JSONB NOT NULL DEFAULT '{}',
    use_count     BIGINT NOT NULL DEFAULT 0,
    is_active     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_templates_trending ON flick_templates(use_count DESC) WHERE is_active = TRUE;

-- Media overlays (per post)
CREATE TABLE IF NOT EXISTS media_overlays (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id          UUID NOT NULL,
    type             TEXT NOT NULL CHECK (type IN ('text','sticker','poll','question','countdown','mention','location','product','link')),
    appears_at_ms    INT NOT NULL DEFAULT 0,
    disappears_at_ms INT,
    position_x       REAL NOT NULL,
    position_y       REAL NOT NULL,
    scale            REAL NOT NULL DEFAULT 1.0,
    rotation         REAL NOT NULL DEFAULT 0,
    data             JSONB NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_overlays_post ON media_overlays(post_id);

-- Export jobs
CREATE TABLE IF NOT EXISTS export_jobs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL,
    status        TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','processing','completed','failed')),
    input_data    JSONB NOT NULL DEFAULT '{}',
    output_path   TEXT,
    error_message TEXT,
    progress      INT NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at  TIMESTAMPTZ,
    expires_at    TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours'
);
CREATE INDEX IF NOT EXISTS idx_export_jobs_user ON export_jobs(user_id, created_at DESC);

-- User filter presets
CREATE TABLE IF NOT EXISTS user_filter_presets (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    name        TEXT NOT NULL,
    adjustments JSONB NOT NULL DEFAULT '{}',
    lut_url     TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
