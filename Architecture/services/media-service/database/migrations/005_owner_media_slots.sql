-- owner_media_slots: Generic slot system for entity media (avatar, banner, watermark, etc.)
CREATE TABLE IF NOT EXISTS owner_media_slots (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_type      TEXT NOT NULL,          -- 'profile', 'channel', 'group', 'module_profile'
    owner_id        UUID NOT NULL,
    slot_name       TEXT NOT NULL,           -- 'avatar', 'banner', 'watermark', 'intro_video', 'cover'
    media_asset_id  UUID NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
    status          TEXT NOT NULL DEFAULT 'pending',  -- 'pending', 'active', 'replaced', 'removed'
    crop_x          DOUBLE PRECISION,       -- normalized 0.0-1.0
    crop_y          DOUBLE PRECISION,
    crop_w          DOUBLE PRECISION,
    crop_h          DOUBLE PRECISION,
    focal_x         DOUBLE PRECISION DEFAULT 0.5,
    focal_y         DOUBLE PRECISION DEFAULT 0.5,
    assigned_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    replaced_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Only one active slot per owner+slot_name
CREATE UNIQUE INDEX IF NOT EXISTS idx_owner_media_slots_active
    ON owner_media_slots (owner_type, owner_id, slot_name) WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_owner_media_slots_owner
    ON owner_media_slots (owner_type, owner_id);

CREATE INDEX IF NOT EXISTS idx_owner_media_slots_media
    ON owner_media_slots (media_asset_id);

-- owner_media_resolved: Materialized fast-read table for active slots with variant info
CREATE TABLE IF NOT EXISTS owner_media_resolved (
    owner_type      TEXT NOT NULL,
    owner_id        UUID NOT NULL,
    slot_name       TEXT NOT NULL,
    media_asset_id  UUID NOT NULL,
    blurhash        TEXT,
    width           INT,
    height          INT,
    variants        JSONB NOT NULL DEFAULT '{}',   -- {"thumb_150": "object_key", "small_480": "object_key", ...}
    focal_x         DOUBLE PRECISION DEFAULT 0.5,
    focal_y         DOUBLE PRECISION DEFAULT 0.5,
    resolved_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (owner_type, owner_id, slot_name)
);

CREATE INDEX IF NOT EXISTS idx_owner_media_resolved_media
    ON owner_media_resolved (media_asset_id);
