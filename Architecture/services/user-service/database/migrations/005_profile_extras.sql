-- Profile pins: featured content on a user's profile
CREATE TABLE IF NOT EXISTS profile_pins (
    user_id         UUID NOT NULL,
    content_type    TEXT NOT NULL CHECK (content_type IN ('post','reel','video','product')),
    content_id      UUID NOT NULL,
    pin_order       INT NOT NULL DEFAULT 0,
    pinned_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, content_type, content_id)
);

-- Portfolio items: showcase projects, articles, videos, etc.
CREATE TABLE IF NOT EXISTS portfolio_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    title           TEXT NOT NULL,
    description     TEXT,
    type            TEXT NOT NULL CHECK (type IN ('project','article','video','design','achievement')),
    url             TEXT,
    media_id        UUID,
    sort_order      INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_portfolio_user ON portfolio_items(user_id, sort_order);

-- Profile QR codes: shareable QR links
CREATE TABLE IF NOT EXISTS profile_qr_codes (
    user_id     UUID PRIMARY KEY,
    qr_url      TEXT NOT NULL,
    short_link  TEXT NOT NULL,
    scan_count  BIGINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Digital wellbeing settings
CREATE TABLE IF NOT EXISTS digital_wellbeing (
    user_id             UUID PRIMARY KEY,
    daily_limit_mins    INT,
    focus_mode_enabled  BOOLEAN NOT NULL DEFAULT FALSE,
    focus_mode_until    TIMESTAMPTZ,
    bedtime_start       TIME,
    bedtime_end         TIME,
    nudge_interval_mins INT NOT NULL DEFAULT 30,
    hide_like_counts    BOOLEAN NOT NULL DEFAULT FALSE,
    detox_mode_until    TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Screen time log: daily usage tracking
CREATE TABLE IF NOT EXISTS screen_time_log (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL,
    date          DATE NOT NULL,
    minutes       INT NOT NULL DEFAULT 0,
    session_count INT NOT NULL DEFAULT 0,
    UNIQUE (user_id, date)
);
CREATE INDEX IF NOT EXISTS idx_screen_time_user ON screen_time_log(user_id, date DESC);
