-- Appeals
CREATE TABLE IF NOT EXISTS trust.content_appeals (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    content_type    TEXT NOT NULL,
    content_id      UUID NOT NULL,
    action_taken    TEXT NOT NULL,
    appeal_reason   TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open','under_review','upheld','overturned','expired')),
    reviewed_by     UUID,
    resolution_note TEXT,
    submitted_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_appeals_user ON trust.content_appeals(user_id, status);
CREATE INDEX IF NOT EXISTS idx_appeals_status ON trust.content_appeals(status, submitted_at DESC);

-- Anti-harassment keyword filters
CREATE TABLE IF NOT EXISTS trust.keyword_filters (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope       TEXT NOT NULL CHECK (scope IN ('platform','group','channel','user')),
    scope_id    UUID,
    keyword     TEXT NOT NULL,
    action      TEXT NOT NULL DEFAULT 'hide' CHECK (action IN ('hide','flag','block')),
    added_by    UUID NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_keyword_filters_scope ON trust.keyword_filters(scope, scope_id);

-- Teen safety accounts
CREATE TABLE IF NOT EXISTS trust.teen_accounts (
    user_id             UUID PRIMARY KEY,
    guardian_id         UUID,
    guardian_approved   BOOLEAN NOT NULL DEFAULT FALSE,
    daily_limit_mins    INT NOT NULL DEFAULT 60,
    content_filter      TEXT NOT NULL DEFAULT 'strict' CHECK (content_filter IN ('strict','moderate','off')),
    dm_restricted       BOOLEAN NOT NULL DEFAULT TRUE,
    follower_approval   BOOLEAN NOT NULL DEFAULT TRUE,
    location_hidden     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Media labels (deepfake/AI-generated)
CREATE TABLE IF NOT EXISTS trust.media_labels (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    media_asset_id  UUID NOT NULL,
    label_type      TEXT NOT NULL CHECK (label_type IN ('ai_generated','deepfake','edited','satire','synthetic_audio')),
    confidence      REAL NOT NULL,
    source          TEXT NOT NULL CHECK (source IN ('auto_detected','user_reported','admin_labelled')),
    labeled_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_media_labels_asset ON trust.media_labels(media_asset_id);

-- Strike system
CREATE TABLE IF NOT EXISTS trust.user_strikes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    reason          TEXT NOT NULL,
    content_type    TEXT,
    content_id      UUID,
    severity        TEXT NOT NULL CHECK (severity IN ('warning','strike','severe_strike')),
    expires_at      TIMESTAMPTZ,
    created_by      UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_strikes_user ON trust.user_strikes(user_id, created_at DESC);

-- Verification requests
CREATE TABLE IF NOT EXISTS trust.verification_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    type            TEXT NOT NULL CHECK (type IN ('creator','business','organization','government')),
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','more_info_needed')),
    submitted_docs  JSONB,
    rejection_reason TEXT,
    reviewed_by     UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
